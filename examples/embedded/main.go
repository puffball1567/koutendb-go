//go:build cgo && kouten_embedded

package main

import (
	"fmt"
	"log"

	"github.com/puffball1567/koutendb-go/embedded"
)

func main() {
	db, err := embedded.Open(8)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	id, err := db.PutJSON("users/123/profile", map[string]string{"name": "Ada"})
	if err != nil {
		log.Fatal(err)
	}
	var user struct{ Name string }
	found, err := db.GetJSON(id, &user)
	if err != nil || !found {
		log.Fatalf("get: found=%v error=%v", found, err)
	}
	page, err := db.ReadRing("users/123/profile", embedded.RingOptions{Selection: "{ name }", Limit: 10})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(user.Name, string(page))
}
