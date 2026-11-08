package links

// Events reported through Pool.OnChange.
const (
	EventAdded        = "added"                   // a new interface appeared
	EventReconnected  = "reconnected"             // a known interface is back
	EventDisconnected = "disconnected"            // the interface went away
	EventAnswering    = "answering again"         // internet checks succeed again
	EventNotAnswering = "not answering (skipped)" // internet checks keep failing
)
