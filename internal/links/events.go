package links

// Events reported through Pool.OnChange.
const (
	EventAdded        = "added"                   // a new interface appeared
	EventReconnected  = "reconnected"             // a known interface is back
	EventDisconnected = "disconnected"            // the interface went away
	EventAnswering    = "answering again"         // internet checks succeed again
	EventNotAnswering = "not answering (skipped)" // internet checks keep failing
	EventBudgetWarn   = "budget warning"          // the warning share of the monthly budget is used
	EventOverBudget   = "over budget (skipped)"   // monthly budget used: no new connections
	EventBudgetOK     = "budget available again"  // new month, or budget raised
	EventBudgetKept   = "over budget (kept)"      // over budget but the only link left
)
