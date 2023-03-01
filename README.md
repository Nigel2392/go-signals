# Go-Signals

A simple package for easily sending signals application-wide.
Signals are a way to communicate between different parts of your application.

## Installation
```bash
go get github.com/Nigel2392/go-signals
``` 

## Synchronously sending signals
```go
// Create a new signal in the global pool,
// if it was already created, we will fetch the old one
// from the pool.
var signal = signals.Get("mysignal")

var messages = make([]string, 0)

// Initialize a receiver
var receiver = signals.NewRecv(func(signal signals.Signal, value ...any) error {
	t.Logf("Received %v from %s", value, signal.Name())
	messages = append(messages, value[0].(string))
	return nil
})

// Connect a receiver to a signal
signal.Connect(receiver)

// Send a signal
var err = signal.Send("This is a signal message!")
if err != nil {
	t.Errorf("Expected no errors, got %s", err.Error())
}

// Disconnect a receiver from a signal.
signal.Disconnect(receiver)
```

## Asynchronously sending signals
```go
var signal = signals.Get("mysignal")
var errChan = signal.SendAsync("This is a signal message!")
for err := range errChan {
	if err != nil {
		fmt.Printf("Received error: %s\n", err.Error())
	}
}

```