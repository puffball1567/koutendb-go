package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	kouten "github.com/puffball1567/koutendb-go"
)

func main() {
	endpoint := os.Getenv("KOUTENDB_ENDPOINT")
	if endpoint == "" {
		endpoint = "127.0.0.1:17301"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := kouten.Dial(ctx, []string{endpoint}, kouten.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	id, err := db.PutJSON(ctx, "examples/go", map[string]string{"title": "Go TCP example"})
	if err != nil {
		log.Fatal(err)
	}
	var article struct{ Title string }
	found, err := db.GetJSON(ctx, id, &article)
	if err != nil || !found {
		log.Fatalf("get: found=%v error=%v", found, err)
	}
	fmt.Println(id.String(), article.Title)
}
