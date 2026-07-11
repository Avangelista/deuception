// Package protocol defines the messages the room pushes into each session's
// Bubble Tea program and the per-viewer, redacted view of game state.
package protocol

import (
	"time"

	"github.com/Avangelista/deuception/internal/game"
)

// RevealHold is the standard beat a resolved moment rests on screen before the
// next state takes over: a won trick before it clears, the winning play before the
// scoreboard, a card landing before its trick resets. Kept in one place so every
// reveal feels the same, on both the server (trick hold) and client (pile hold).
const RevealHold = 600 * time.Millisecond

// Phase mirrors the room lifecycle for rendering.
type Phase int

const (
	Waiting Phase = iota
	InGame
	Finished
)

// StateSnapshotMsg carries a fresh, per-viewer redacted view of the room.
type StateSnapshotMsg struct{ Snap StateSnapshot }

// ErrorMsg is an inline hint shown to a single player (e.g. an illegal move).
type ErrorMsg struct{ Text string }

// KickedMsg tells a connection it cannot join (game in progress / full).
type KickedMsg struct{ Reason string }

// RoomClosedMsg tells a session the room is gone (host quit / server shutdown).
type RoomClosedMsg struct{}

// StateSnapshot is one viewer's redacted view: opponents' cards are counts only,
// never the cards themselves.
type StateSnapshot struct {
	Phase Phase
	Rev   int // monotonic per room; a client ignores a snapshot older than the last

	YouSeat  int
	IsHost   bool
	MaxSeats int
	MinStart int

	Players  []PlayerView
	YourHand []game.Card // recipient's own hand (full)
	Table    []game.Card // current winning play (public)
	Turn     int         // -1 outside of play
	TableBy  int         // seat that played the current Table combo; -1 on a new trick
	Winner   int         // -1 until the hand is won
}

// PlayerView is the redacted, public view of one seat.
type PlayerView struct {
	Seat      int
	Letter    byte // chosen display letter, unique per room
	CardCount int  // count only - never the cards
	Connected bool
	IsYou     bool
	IsTurn    bool
	IsHost    bool
	IsBot     bool
	BotLevel  int  // 1-9 difficulty when IsBot; 0 otherwise
	Passed    bool // passed (locked out) in the current trick
	Score     int
}
