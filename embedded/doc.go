// Package embedded provides the optional KoutenDB C ABI wrapper.
// Build with cgo and -tags=kouten_embedded. Link against libkoutendb ABI v2.
// A DB owns a dedicated OS thread for serialized ABI calls and error capture.
// Always call Close to release the handle and its worker.
package embedded
