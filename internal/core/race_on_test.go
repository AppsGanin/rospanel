//go:build race

package core

// raceEnabled scales the heaviest tests down under the race detector, where SQLite (pure
// Go here, behind one allocator lock) runs many times slower.
const raceEnabled = true
