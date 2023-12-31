// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package clique

import (
	"bytes"
	"encoding/json"
	"math/big"
	"sort"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	lru "github.com/hashicorp/golang-lru"
)

// Vote represents a single vote that an authorized signer made to modify the
// list of authorizations.
type Vote struct {
	Signer    common.Address `json:"signer"`    // Authorized signer that cast this vote
	Block     uint64         `json:"block"`     // Block number the vote was cast in (expire old votes)
	Address   common.Address `json:"address"`   // Account being voted on to change its authorization
	Authorize bool           `json:"authorize"` // Whether to authorize or deauthorize the voted account
}

type LimitVote struct {
	Signer    common.Address `json:"signer"`    // Authorized signer that cast this vote
	Block     uint64         `json:"block"`     // Block number the vote was cast in (expire old votes)
	Limit     uint           `json:"limit"`   
	Address   common.Address `json:"address"`  // Account being voted on to change its authorization
	Authorize bool           `json:"authorize"` // Whether to authorize or deauthorize the voted account
}

// Tally is a simple vote tally to keep the current score of votes. Votes that
// go against the proposal aren't counted since it's equivalent to not voting.
type Tally struct {
	Authorize bool `json:"authorize"` // Whether the vote is about authorizing or kicking someone
	Votes     int  `json:"votes"`     // Number of votes until now wanting to pass the proposal
}

type LimitTally struct {
	Authorize bool `json:"authorize"` // Whether the vote is about authorizing or kicking someone
	Votes     int  `json:"votes"`     // Number of votes until now wanting to pass the proposal
	Signer    common.Address `json:"signer"`
}

type WaitTally struct {
	Block     uint64  `json:"wait"`     // Number of blocks for the next proposal
}

// Snapshot is the state of the authorization voting at a given point in time.
type Snapshot struct {
	config   *params.CliqueConfig // Consensus engine parameters to fine tune behavior
	sigcache *lru.ARCCache        // Cache of recent block signatures to speed up ecrecover

	Number  uint64                      `json:"number"`  // Block number where the snapshot was created
	Hash    common.Hash                 `json:"hash"`    // Block hash where the snapshot was created
	Signers map[common.Address]struct{} `json:"signers"` // Set of authorized signers at this moment
	Recents map[uint64]common.Address   `json:"recents"` // Set of recent signers for spam protections
	Votes   []*Vote                     `json:"votes"`   // List of votes cast in chronological order
	Tally   map[common.Address]Tally    `json:"tally"`   // Current vote tally to avoid recalculating

	SignerLimit      uint               `json:"limit"`            // Current vote tally to avoid recalculating
	SignerLimitVotes []*LimitVote       `json:"signerLimitVotes"` // List of votes cast in chronological order
	SignerLimitTally map[uint]LimitTally `json:"signerLimitTally"`
	SignerLimitWait  map[uint64]WaitTally `json:"waitTally"`
}

// signersAscending implements the sort interface to allow sorting a list of addresses
type signersAscending []common.Address

func (s signersAscending) Len() int           { return len(s) }
func (s signersAscending) Less(i, j int) bool { return bytes.Compare(s[i][:], s[j][:]) < 0 }
func (s signersAscending) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// newSnapshot creates a new snapshot with the specified startup parameters. This
// method does not initialize the set of recent signers, so only ever use if for
// the genesis block.
func newSnapshot(config *params.CliqueConfig, sigcache *lru.ARCCache, number uint64, hash common.Hash, signers []common.Address) *Snapshot {
	snap := &Snapshot{
		config:           config,
		sigcache:         sigcache,
		Number:           number,
		Hash:             hash,
		Signers:          make(map[common.Address]struct{}),
		Recents:          make(map[uint64]common.Address),
		Tally:            make(map[common.Address]Tally),
		SignerLimit:      50,
		SignerLimitTally: make(map[uint]LimitTally),
		SignerLimitWait:  make(map[uint64]WaitTally),
	}
	for _, signer := range signers {
		snap.Signers[signer] = struct{}{}
	}
	return snap
}

// loadSnapshot loads an existing snapshot from the database.
func loadSnapshot(config *params.CliqueConfig, sigcache *lru.ARCCache, db ethdb.Database, hash common.Hash) (*Snapshot, error) {
	blob, err := db.Get(append([]byte("clique-"), hash[:]...))
	if err != nil {
		return nil, err
	}
	snap := new(Snapshot)
	if err := json.Unmarshal(blob, snap); err != nil {
		return nil, err
	}
	snap.config = config
	snap.sigcache = sigcache

	return snap, nil
}

// store inserts the snapshot into the database.
func (s *Snapshot) store(db ethdb.Database) error {
	blob, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return db.Put(append([]byte("clique-"), s.Hash[:]...), blob)
}

// copy creates a deep copy of the snapshot, though not the individual votes.
func (s *Snapshot) copy() *Snapshot {
	cpy := &Snapshot{
		config:           s.config,
		sigcache:         s.sigcache,
		Number:           s.Number,
		Hash:             s.Hash,
		Signers:          make(map[common.Address]struct{}),
		Recents:          make(map[uint64]common.Address),
		Votes:            make([]*Vote, len(s.Votes)),
		SignerLimitVotes: make([]*LimitVote, len(s.SignerLimitVotes)),
		Tally:            make(map[common.Address]Tally),
		SignerLimitTally: make(map[uint]LimitTally),
		SignerLimitWait:  make(map[uint64]WaitTally),
	}
	for signer := range s.Signers {
		cpy.Signers[signer] = struct{}{}
	}
	for block, signer := range s.Recents {
		cpy.Recents[block] = signer
	}
	for address, tally := range s.Tally {
		cpy.Tally[address] = tally
	}
	copy(cpy.Votes, s.Votes)

	cpy.SignerLimit = s.SignerLimit

	for address, tally := range s.SignerLimitTally {
		cpy.SignerLimitTally[address] = tally
	}

	copy(cpy.SignerLimitVotes, s.SignerLimitVotes)

	for number, tally := range s.SignerLimitWait {
		cpy.SignerLimitWait[number] = tally
	}
	return cpy
}

// validVote returns whether it makes sense to cast the specified vote in the
// given snapshot context (e.g. don't try to add an already authorized signer).
func (s *Snapshot) validVote(address common.Address, authorize bool) bool {
	_, signer := s.Signers[address]
	return (signer && !authorize) || (!signer && authorize)
}

func (s *Snapshot) validSignerLimitVote(signerLimit uint, authorize bool) bool {
	return authorize && s.SignerLimit != signerLimit
}

// cast adds a new vote into the tally.
func (s *Snapshot) cast(address common.Address, authorize bool) bool {
	// Ensure the vote is meaningful
	if !s.validVote(address, authorize) {
		return false
	}

	// Cast the vote into an existing or new tally
	if old, ok := s.Tally[address]; ok {
		old.Votes++
		s.Tally[address] = old
	} else {
		s.Tally[address] = Tally{Authorize: authorize, Votes: 1}
	}
	return true
}

func (s *Snapshot) castSignerLimit(address common.Address, signerLimit uint) bool {
	if !s.validSignerLimitVote(signerLimit, true) {
		return false
	}

	if old, ok := s.SignerLimitTally[signerLimit]; ok {
		old.Votes++
		s.SignerLimitTally[signerLimit] = old
	} else {
		s.SignerLimitTally[signerLimit] = LimitTally{Signer: address, Authorize: true, Votes: 1}
	}
	return true
}

// uncast removes a previously cast vote from the tally.
func (s *Snapshot) uncast(address common.Address, authorize bool) bool {
	// If there's no tally, it's a dangling vote, just drop
	tally, ok := s.Tally[address]
	if !ok {
		return false
	}
	// Ensure we only revert counted votes
	if tally.Authorize != authorize {
		return false
	}

	// Otherwise revert the vote
	if tally.Votes > 1 {
		tally.Votes--
		s.Tally[address] = tally
	} else {
		delete(s.Tally, address)
	}
	return true
}

func (s *Snapshot) uncastSignerLimit(signerLimit uint, authorize bool) bool {
	// If there's no tally, it's a dangling vote, just drop
	tally, ok := s.SignerLimitTally[signerLimit]
	if !ok {
		return false
	}
	// Ensure we only revert counted votes
	if tally.Authorize != authorize {
		return false
	}

	// Otherwise revert the vote
	if tally.Votes > 1 {
		tally.Votes--
		s.SignerLimitTally[signerLimit] = tally
	} else {
		delete(s.SignerLimitTally, signerLimit)
	}
	return true
}


func (s *Snapshot, ) applySignerLimitVotes(signer common.Address, snap *Snapshot, header *types.Header) {
	number := header.Number.Uint64()
	limit := uint(new(big.Int).SetBytes(header.Coinbase.Bytes()).Uint64())

	snap.deleteLimitWait()

	if snap.castSignerLimit(signer, limit) {
		snap.SignerLimitVotes = append(snap.SignerLimitVotes, &LimitVote{
			Signer:    signer,
			Block:     number,
			Address:   header.Coinbase,
			Limit:     limit,
			Authorize: true,
		})
	}

	// If the vote passed, update the list of signers
	if tally := snap.SignerLimitTally[limit]; tally.Votes >= int(snap.signerLimit()) {
		snap.SignerLimit = limit
		
		// Discard any previous votes around the just changed account
		for i := 0; i < len(snap.SignerLimitVotes); i++ {
			if snap.SignerLimitVotes[i].Address == header.Coinbase {
				snap.SignerLimitVotes = append(snap.SignerLimitVotes[:i], snap.SignerLimitVotes[i+1:]...)
				i--
			}
		}
		delete(snap.SignerLimitTally, limit)
		
		blockWait := number + uint64(len(s.Signers))
		snap.SignerLimitWait[uint64(limit)] = WaitTally{Block: blockWait}
	}
}

// apply creates a new authorization snapshot by applying the given headers to
// the original one.
func (s *Snapshot) apply(headers []*types.Header) (*Snapshot, error) {

	
	// Allow passing in no headers for cleaner code
	if len(headers) == 0 {
		return s, nil
	}
	// Sanity check that the headers can be applied
	for i := 0; i < len(headers)-1; i++ {
		if headers[i+1].Number.Uint64() != headers[i].Number.Uint64()+1 {
			return nil, errInvalidVotingChain
		}
	}
	if headers[0].Number.Uint64() != s.Number+1 {
		return nil, errInvalidVotingChain
	}
	// Iterate through the headers and create a new snapshot
	snap := s.copy()

	var (
		start  = time.Now()
		logged = time.Now()
	)

	
	for i, header := range headers {
		// Remove any votes on checkpoint blocks
		number := header.Number.Uint64()
		if number%s.config.Epoch == 0 {
			snap.Votes = nil
			snap.Tally = make(map[common.Address]Tally)

			snap.SignerLimitVotes = nil
			snap.SignerLimitTally = make(map[uint]LimitTally)
		}

		// Delete the oldest signer from the recent list to allow it signing again
		snap.shrunkRecents(number)

		// Resolve the authorization key and check against signers
		signer, err := ecrecover(header, s.sigcache)
		if err != nil {
			return nil, err
		}
		if _, ok := snap.Signers[signer]; !ok {
			return nil, errUnauthorizedSigner
		}

		for _, recent := range snap.Recents {
			if recent == signer {
				return nil, errRecentlySigned
			}
		}
		snap.Recents[number] = signer

		limit := uint(new(big.Int).SetBytes(header.Coinbase.Bytes()).Uint64())

		//discard previous votes for limit
		for i, vote := range snap.SignerLimitVotes{
			if vote.Signer == signer && vote.Address == header.Coinbase && vote.Limit == limit {
				snap.uncastSignerLimit(limit, true)

				// Uncast the vote from the chronological list
				snap.SignerLimitVotes = append(snap.SignerLimitVotes[:i], snap.SignerLimitVotes[i+1:]...)
				break // only one vote allowed
			}
		
		}

		// Header authorized, discard any previous votes from the signer
		for i, vote := range snap.Votes {
			if vote.Signer == signer && vote.Address == header.Coinbase {
				// Uncast the vote from the cached tally
				snap.uncast(vote.Address, vote.Authorize)

				// Uncast the vote from the chronological list
				snap.Votes = append(snap.Votes[:i], snap.Votes[i+1:]...)
				break // only one vote allowed
			}
		}

		// Tally up the new vote from the signer
		var authorize bool
		switch {
		case bytes.Equal(header.Nonce[:], nonceAuthVote):
			authorize = true
		case bytes.Equal(header.Nonce[:], nonceDropVote):
			authorize = false
		case bytes.Equal(header.Nonce[:], nonceSignerLimitAuthVote):
			s.applySignerLimitVotes(signer, snap, header)
		default:
			return nil, errInvalidVote
		}

		if snap.cast(header.Coinbase, authorize) {
			snap.Votes = append(snap.Votes, &Vote{
				Signer:    signer,
				Block:     number,
				Address:   header.Coinbase,
				Authorize: authorize,
			})
		}

		// If the vote passed, update the list of signers
		if tally := snap.Tally[header.Coinbase]; tally.Votes >= int(snap.signerLimit()) {
			if tally.Authorize {
				snap.Signers[header.Coinbase] = struct{}{}
			} else {
				delete(snap.Signers, header.Coinbase)

				// Signer list shrunk, delete any leftover recent caches
				snap.shrunkRecents(number)

				// Discard any previous votes the deauthorized signer cast
				for i := 0; i < len(snap.Votes); i++ {
					if snap.Votes[i].Signer == header.Coinbase {
						// Uncast the vote from the cached tally
						snap.uncast(snap.Votes[i].Address, snap.Votes[i].Authorize)

						// Uncast the vote from the chronological list
						snap.Votes = append(snap.Votes[:i], snap.Votes[i+1:]...)

						i--
					}
				}
			}
			// Discard any previous votes around the just changed account
			for i := 0; i < len(snap.Votes); i++ {
				if snap.Votes[i].Address == header.Coinbase {

					snap.Votes = append(snap.Votes[:i], snap.Votes[i+1:]...)
					i--
				}
			}

			delete(snap.Tally, header.Coinbase)
		}
		// If we're taking too much time (ecrecover), notify the user once a while
		if time.Since(logged) > 8*time.Second {
			log.Info("Reconstructing voting history", "processed", i, "total", len(headers), "elapsed", common.PrettyDuration(time.Since(start)))
			logged = time.Now()
		}
	}
	if time.Since(start) > 8*time.Second {
		log.Info("Reconstructed voting history", "processed", len(headers), "elapsed", common.PrettyDuration(time.Since(start)))
	}
	snap.Number += uint64(len(headers))
	snap.Hash = headers[len(headers)-1].Hash()

	return snap, nil
}

// signers retrieves the list of authorized signers in ascending order.
func (s *Snapshot) signers() []common.Address {
	sigs := make([]common.Address, 0, len(s.Signers))
	for sig := range s.Signers {
		sigs = append(sigs, sig)
	}
	sort.Sort(signersAscending(sigs))
	return sigs
}

// inturn returns if a signer at a given block height is in-turn or not.
func (s *Snapshot) inturn(number uint64, signer common.Address) bool {
	signers, offset := s.signers(), 0
	for offset < len(signers) && signers[offset] != signer {
		offset++
	}
	return (number % uint64(len(signers))) == uint64(offset)
}

func (s *Snapshot) signerLimit() uint {
	return uint(len(s.Signers))*s.SignerLimit/100 + 1
}

func (s *Snapshot) deleteLimitWait(){
	for i := range s.SignerLimitWait {
		delete(s.SignerLimitWait, i)
	}
}

func (s *Snapshot) shrunkRecents(number uint64) {
	if limit := uint64(s.signerLimit()); number >= limit {
		recentsSize := uint64(len(s.Recents))
		if recentsSize >= limit {
			deleteAmount := recentsSize - limit + 1
			var i uint64
			for i = 0; i < deleteAmount; i++ {
				delete(s.Recents, number-limit-i)
			}
		}
	}
}