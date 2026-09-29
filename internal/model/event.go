package model

// EventKind identifies what a streamed Event carries.
type EventKind int

const (
	EventTextDelta      EventKind = iota // Text: the next piece of the answer
	EventReasoningDelta                  // Reasoning: the next piece of the model's thinking
	EventToolCall                        // ToolCall: one complete tool call
	EventDone                            // the response ended; Usage and Truncated may be set
	EventError                           // Err: the response failed partway
	// EventReclassify says the text streamed so far in this response was
	// really reasoning (a template opened <think> in the prompt, so only the
	// closing tag arrived). Consumers move that text to reasoning.
	EventReclassify
)

// Event is one unit of a streamed response. Which fields are set depends
// on Kind.
type Event struct {
	Kind      EventKind
	Text      string
	Reasoning string
	ToolCall  *ToolCall
	Usage     *Usage // on EventDone, when the provider reports it
	Truncated bool   // on EventDone: the response hit the output-token limit
	Err       error
}

// Usage is the provider-reported token accounting for one round-trip.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CachedTokens     int // prompt tokens served from the provider's cache; 0 if unreported
	// Cost is what the endpoint billed in US dollars, when it says
	// (OpenRouter does): the actual provider's rates, cache discounts included.
	Cost         float64
	CostReported bool   // Cost is from the endpoint, so a 0 means free, not unknown
	Provider     string // the upstream provider that served it, when a router says
}
