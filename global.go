package signals

// Default signal pool
//
// This will be used to store, retrieve and delete signals.
//
// New signals will be created and added to the pool if they do not exist.
//
// If you need a separate pool of signals, use NewPool() to create a new one!
var signals = NewPool()

// This will send the signal to all receivers that are connected to the signal.
//
// Returns an error, if any of the receivers return an error.
func Send(name string, value any) error {
	return signals.Send(name, value)
}

// Send a signal globally.
//
// This will send a signal across multiple signals, to ALL receivers.
//
// Returns an error, if any of the receivers return an error.
func SendGlobal(value ...any) error {
	return signals.SendGlobal(value...)
}

// Get a signal by name.
//
// Create a new one if it does not exist.
func Get(name string) Signal {
	return signals.Get(name)
}

//	Create or send a signal inside of the signal pool.
//
//	This will send a signal to the receivers, if the signal already exists.
func CreateOrSend(name string, value ...any) error {
	return signals.CreateOrSend(name, value)
}

//	Register a receiver to a signal.
//
//	This will register a receiver to a signal inside of the pool.
//
//	If the signal does not exist, it will be created.
//
//	This is a shorthand.
func Listen(name string, r func(Signal, ...any) error) {
	signals.Listen(name, r)
}
