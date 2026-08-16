package main

// Integer settings the SMB35 client asks for via Utility::GetIntegerSettings
// (index 0 and 10). Values ported verbatim from kinnay/SMB35's source/config.py
// — indices are undocumented on the wire (no field names survive), so these are
// kept exactly as the reference had them rather than guessed at.
var integerSettings1 = map[uint16]int32{
	0: 60, 1: 30, 2: 90, 3: 1, 4: 0, 5: 0, 6: 0, 7: 0,
	8: 0, 9: 0, 10: 5, 11: 3, 12: 1, 13: 30, 14: 30, 15: 180, 16: 0,
}

// integerSettings2 (index 10) was an open question in the reference too
// ("What integers should we use here?") — left empty, same as upstream.
var integerSettings2 = map[uint16]int32{}
