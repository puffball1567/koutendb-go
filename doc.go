// Package koutendb provides a native Go TCP client for KoutenDB wire v1.
// It does not require cgo or libkoutendb. Clients serialize operations and may
// be shared by goroutines. Use separate clients for parallel requests.
package koutendb
