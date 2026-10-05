package chat

// InputDelivery describes acceptance, not the model-visible message role.
// Hosts that accept text into a queue call AcceptHumanMessage at enqueue time
// and retain InputAccepted through draining, retries, and undelivered redispatch.
type InputDelivery uint8

const (
	// InputHuman is fresh human conversation text; accept it once on delivery.
	InputHuman InputDelivery = iota
	// InputAutomatic is generated input, never a human reset or undelivered item.
	InputAutomatic
	// InputAccepted is human input whose acceptance was already reported.
	InputAccepted
)
