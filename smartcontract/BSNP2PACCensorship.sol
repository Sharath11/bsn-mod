//SPDX-License-Identifier: Unlicense
pragma solidity ^0.8.0;

import "@openzeppelin/contracts/utils/structs/EnumerableSet.sol";
import "@openzeppelin/contracts/access/Ownable.sol";

interface IBSNCensorship {
    function blacklist() external view returns (address[] memory);
    function addToBlacklist(address addr) external;
    function removeFromBlacklist(address addr) external;
    function isInBlacklist(address addr) external view returns (bool);

    function whitelist() external view returns (address[] memory);
    function addToWhitelist(address addr) external;
    function removeFromWhitelist(address addr) external;
    function isInWhitelist(address addr) external view returns (bool);

    function nodeList() external view returns (string[] memory);
    function addEnode(string memory enodrURL) external;
    function removeEnode(string memory enodeURL) external;
    function isAuthorized(string memory enodeURL) external view returns (bool);

    event UpdateCensorshipContract(address indexed sender, address contractAddress);
    event NodeAdded(address indexed sender, string enode);
    event NodeRemoved(address indexed sender, string enode);
}

contract BSNP2PACCensorship is IBSNCensorship, Ownable {
    using EnumerableSet for EnumerableSet.AddressSet;
    using EnumerableSet for EnumerableSet.Bytes32Set;
    EnumerableSet.AddressSet private _blacklistSet;
    EnumerableSet.AddressSet private _whitelistSet;
    EnumerableSet.Bytes32Set private _nodelistSet;
    uint public _effectiveTimestamp;

    mapping(bytes32 => string) enodeUrl;  
    mapping(bytes32 => uint) enodeIdCounter;

    constructor(
        address[] memory __whitelist,
        address[] memory __blacklist,
        string[] memory __nodeList,
        uint __effectiveTimestamp
    ){
        for(uint i = 0; i < __whitelist.length; i ++){
            _whitelistSet.add(__whitelist[i]);
        }
        for(uint i = 0; i < __blacklist.length; i ++){
            _blacklistSet.add(__blacklist[i]);
        }
        for(uint i = 0; i < __nodeList.length; i ++){
            string memory enodeURL = __nodeList[i];
            bytes32 stringInBytes32 = bytes32(keccak256(bytes(enodeURL)));
            _nodelistSet.add(stringInBytes32);
            enodeUrl[stringInBytes32] = enodeURL;
            enodeIdCounter[bytes32(keccak256(_enodeID(enodeURL)))] ++;
            emit NodeAdded(msg.sender, enodeURL);
        }
        _effectiveTimestamp = __effectiveTimestamp;
    }

    function blacklist() override external view returns (address[] memory) {
        if(block.timestamp >= _effectiveTimestamp){
            address[] memory _list = new address[](_blacklistSet.length());
            for(uint i = 0; i < _blacklistSet.length(); i ++){
                _list[i] = _blacklistSet.at(i);
            }
            return _list;
        }else{
            return new address[](0);
        }
    }

    function addToBlacklist(address addr) onlyOwner() override external {
        _blacklistSet.add(addr);
    }

    function removeFromBlacklist(address addr) onlyOwner() override external {
        _blacklistSet.remove(addr);
    }

    function isInBlacklist(address addr) override external view returns (bool) {
        if(block.timestamp >= _effectiveTimestamp){
            return _blacklistSet.contains(addr);
        }else{
            return false;
        }
    }

    function whitelist() override external view returns (address[] memory) {
        address[] memory _list = new address[](_whitelistSet.length());
        for(uint i = 0; i < _whitelistSet.length(); i ++){
            _list[i] = _whitelistSet.at(i);
        }
        return _list;
    }

    function addToWhitelist(address addr) onlyOwner() override external {
        _whitelistSet.add(addr);
    }

    function removeFromWhitelist(address addr) onlyOwner() override external {
        _whitelistSet.remove(addr);
    }

    function isInWhitelist(address addr) override external view returns (bool) {
        return _whitelistSet.contains(addr);
    }

    function nodeList() override external view returns (string[] memory) {
        string[] memory _list = new string[](_nodelistSet.length());
        for(uint i = 0; i < _nodelistSet.length(); i++){
            _list[i] = enodeUrl[_nodelistSet.at(i)];
        }
        return _list;
    }

    function _isAuthorized(string memory enode) private view returns (bool) {
        bytes32 stringInBytes32 = bytes32(keccak256(bytes(enode)));
        return _nodelistSet.contains(stringInBytes32);
    }

    function _enodeID(string memory enode) private pure returns (bytes memory) {
        bytes memory enodeBytes = bytes(enode);
        bytes memory id = new bytes(128);
        uint i = 8;
        for(uint j = 0; j < 128; j ++){
            id[j] = enodeBytes[i + j];
        }
        return id;
    }

    function addEnode(string memory enodeURL) onlyOwner() override external {
        bytes32 stringInBytes32 = bytes32(keccak256(bytes(enodeURL)));
        _nodelistSet.add(stringInBytes32);
        enodeUrl[stringInBytes32] = enodeURL;
        enodeIdCounter[bytes32(keccak256(_enodeID(enodeURL)))] ++;
        emit NodeAdded(msg.sender, enodeURL);
    }

    function removeEnode(string memory enodeURL) onlyOwner() override external {
        require (_isAuthorized(enodeURL));
        bytes32 stringInBytes32 = bytes32(keccak256(bytes(enodeURL)));
        _nodelistSet.remove(stringInBytes32);
        delete enodeUrl[stringInBytes32];
        enodeIdCounter[bytes32(keccak256(_enodeID(enodeURL)))] --;
        emit NodeRemoved(msg.sender, enodeURL);
    }

    function isAuthorized(string memory enodeURL) override external view returns (bool) {
        return enodeIdCounter[bytes32(keccak256(_enodeID(enodeURL)))] > 0;
    }

    function updateCensorshipContract(address contractAddress) onlyOwner() external {
        emit UpdateCensorshipContract(msg.sender, contractAddress);
    }
}
