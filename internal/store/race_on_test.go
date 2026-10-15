//go:build race

package store

// raceEnabled scales the heaviest tests down under the race detector, which runs
// SQLite (pure Go here) many times slower; what they assert does not depend on size.
const raceEnabled = true
