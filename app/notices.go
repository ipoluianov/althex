package app

import _ "embed"

// Notices are the licences of AltHex and of what is built into it, to be
// reproduced with the binaries (made by build/notices)
//
//go:embed notices.txt
var Notices string
