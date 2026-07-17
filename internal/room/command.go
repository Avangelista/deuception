package room

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Avangelista/big2-tui/internal/game"
)

// Command is a message submitted to a room's actor goroutine; all state mutation
// happens there.
type Command interface{ isCmd() }

// JoinCmd asks to seat a connection. Prog pushes state back to it; Host marks the
// local host. Identity is a stable cross-session id (SSH key fingerprint, or
// LocalIdentity for the host) used to restore the player's last letter; "" if none.
type JoinCmd struct {
	ID       string
	Identity string
	Prog     *tea.Program
	Host     bool
}

// StartCmd (host only) starts the game if enough players are seated.
type StartCmd struct{ ID string }

// PlayCmd attempts to play the given cards for the sender's seat.
type PlayCmd struct {
	ID    string
	Cards []game.Card
}

// PassCmd passes for the sender's seat.
type PassCmd struct{ ID string }

// NextHandCmd (host only) deals a fresh hand after one finishes, keeping scores.
type NextHandCmd struct{ ID string }

// SetLetterCmd (waiting room) requests the sender's seat display letter.
type SetLetterCmd struct {
	ID     string
	Letter byte
}

// AddBotCmd (host only, waiting room) seats a bot. Bots always play at full strength.
type AddBotCmd struct {
	ID string
}

// RemoveBotCmd (host only, waiting room) removes the most-recently-added bot.
type RemoveBotCmd struct{ ID string }

// BotActCmd fires ~1s after a bot's turn begins; the actor re-checks it is still
// that bot's turn (Seat + Token) before acting.
type BotActCmd struct {
	Seat  int
	Token int
}

// DisconnectCmd marks a seat's connection gone (SSH context cancelled).
type DisconnectCmd struct{ ID string }

// QuitCmd is a graceful leave (player pressed quit).
type QuitCmd struct{ ID string }

// EmoteCmd is a quick-chat reaction: Code indexes the room's reaction labels.
type EmoteCmd struct {
	ID   string
	Code int
}

// SetRulesCmd (host only, waiting room) replaces the house ruleset used for the next
// hand. Ignored once a game is in progress.
type SetRulesCmd struct {
	ID    string
	Rules game.Rules
}

// SetReactionCmd (host only, waiting room) sets reaction label Index to Text
// (<=protocol.MaxReactionLen runes), room-wide. Ignored once a game is in progress.
type SetReactionCmd struct {
	ID    string
	Index int
	Text  string
}

func (JoinCmd) isCmd()        {}
func (StartCmd) isCmd()       {}
func (PlayCmd) isCmd()        {}
func (PassCmd) isCmd()        {}
func (NextHandCmd) isCmd()    {}
func (DisconnectCmd) isCmd()  {}
func (QuitCmd) isCmd()        {}
func (SetLetterCmd) isCmd()   {}
func (AddBotCmd) isCmd()      {}
func (RemoveBotCmd) isCmd()   {}
func (BotActCmd) isCmd()      {}
func (EmoteCmd) isCmd()       {}
func (SetRulesCmd) isCmd()    {}
func (SetReactionCmd) isCmd() {}
