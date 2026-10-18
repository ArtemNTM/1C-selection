package model

import "time"

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return e.Message
}

func NewError(code, message string) error {
	return &Error{
		Code:    code,
		Message: message,
	}
}

type Client struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Role      string    `json:"role"`
	Trust     float64   `json:"trust"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateClientRequest struct {
	Name    string  `json:"name" yaml:"name"`
	Kind    string  `json:"kind" yaml:"kind"`
	Role    string  `json:"role" yaml:"role"`
	Trust   float64 `json:"trust" yaml:"trust"`
	Enabled *bool   `json:"enabled,omitempty" yaml:"enabled"`
}

type UpdateClientRequest struct {
	Trust   *float64 `json:"trust,omitempty"`
	Enabled *bool    `json:"enabled,omitempty"`
}

type ClientCredentials struct {
	Client Client `json:"client"`
	Token  string `json:"token"`
}

type ProvisionClientRequest struct {
	CreateClientRequest `yaml:",inline"`
	Token               string `yaml:"token"`
	TokenFile           string `yaml:"token_file"`
}

type HelloRequest struct {
	ClientID string `json:"client_id"`
	Token    string `json:"token"`
}

type HelloResponse struct {
	Client       Client    `json:"client"`
	SessionToken string    `json:"session_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type NoteRequest struct {
	RequestID string    `json:"request_id"`
	Rubric    string    `json:"rubric"`
	Topic     string    `json:"topic"`
	Body      string    `json:"body,omitempty"`
	EventAt   time.Time `json:"event_at"`
}

type Receipt struct {
	Status      string `json:"status"`
	NoteID      int64  `json:"note_id"`
	StoryID     int64  `json:"story_id"`
	Contributes bool   `json:"contributes"`
	Published   bool   `json:"published"`
}

type Story struct {
	ID             int64         `json:"id"`
	Rubric         string        `json:"rubric"`
	Topic          string        `json:"topic"`
	CreatedAt      time.Time     `json:"created_at"`
	PublishedAt    *time.Time    `json:"published_at"`
	TrustAtPublish *float64      `json:"trust_at_publish"`
	CurrentTrust   float64       `json:"current_trust"`
	Participants   []Participant `json:"participants,omitempty"`
}

type Participant struct {
	ClientID    int64     `json:"client_id"`
	Name        string    `json:"name"`
	FirstNoteID int64     `json:"first_note_id"`
	ReceivedAt  time.Time `json:"received_at"`
	Rank        *int      `json:"rank"`
}

type Notification struct {
	ID             int64      `json:"id"`
	ClientID       int64      `json:"client_id"`
	StoryID        int64      `json:"story_id"`
	Rank           int        `json:"rank"`
	Message        string     `json:"message"`
	CreatedAt      time.Time  `json:"created_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at"`
}

type StoryParticipation struct {
	StoryID     int64      `json:"story_id"`
	Rubric      string     `json:"rubric"`
	Topic       string     `json:"topic"`
	PublishedAt *time.Time `json:"published_at"`
	Rank        *int       `json:"rank"`
}

type Stats struct {
	Client           Client               `json:"client"`
	NotesAccepted    int64                `json:"notes_accepted"`
	NotesPublished   int64                `json:"notes_published"`
	PublicationShare float64              `json:"publication_share"`
	StoriesTouched   int64                `json:"stories_touched"`
	Sensations       int64                `json:"sensations"`
	AverageRank      float64              `json:"average_rank"`
	Stories          []StoryParticipation `json:"stories"`
}
