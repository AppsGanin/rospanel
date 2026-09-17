// Package freeturnref holds Free Turn Proxy's own client-side wire codecs, unchanged
// from github.com/samosvalishe/free-turn-proxy (internal/wire/rtpopus, rtpopus2,
// rtpopus3; MIT, as their SPDX headers say), so the relay's independent implementation
// of that wire (turnrelay/mask.go) is tested against the real thing rather than against
// itself. Only tests import it; nothing here reaches the panel binary.
//
// Copyright (c) 2026 samosvalishe and the free-turn-proxy contributors. Permission is
// hereby granted, free of charge, to any person obtaining a copy of this software and
// associated documentation files (the "Software"), to deal in the Software without
// restriction, including without limitation the rights to use, copy, modify, merge,
// publish, distribute, sublicense, and/or sell copies of the Software, subject to the
// conditions of the original licence, which include keeping this notice.
package freeturnref
