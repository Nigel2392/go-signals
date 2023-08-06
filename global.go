package signals

// Default signal pool
//
// This will be used to store, retrieve and delete signals.
//
// New signals will be created and added to the pool if they do not exist.
//
// If you need a separate pool of signals, use NewPool() to create a new one!
var defaultSignalPool = NewPool[any]()

// This will send the signal to all receivers that are connected to the signal.
//
// Returns an error, if any of the receivers return an error.
func Send(name string, value any) error {
	return defaultSignalPool.Send(name, value)
}

// Get a signal by name.
//
// Create a new one if it does not exist.
func Get(name string) Signal[any] {
	return defaultSignalPool.Get(name)
}

//	Register a receiver to a signal.
//
//	This will register a receiver to a signal inside of the pool.
//
//	If the signal does not exist, it will be created.
//
//	This is a shorthand.
func Listen(name string, r func(Signal[any], any) error) {
	defaultSignalPool.Listen(name, r)
}
