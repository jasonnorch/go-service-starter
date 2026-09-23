// Command apikey generates an API key and prints what to store for it.
//
//	go run ./cmd/apikey -name ci-deploy
//
// The key is printed once and never stored; what is stored is its SHA-256,
// either appended to AUTH_KEYS or inserted into the api_keys table. It needs
// no database or config, so it can run anywhere the key will be handed out.
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"regexp"

	"github.com/jasonnorch/go-service-starter/internal/auth"
)

// validName keeps a key's name safe to embed in both an AUTH_KEYS entry, where
// "," and "=" are syntax, and the SQL printed below.
var validName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func main() {
	name := flag.String("name", "", "a label for the key, such as ci-deploy (letters, digits, . _ -)")
	flag.Parse()

	if !validName.MatchString(*name) {
		fmt.Fprintln(os.Stderr, "apikey: -name is required: 1-64 letters, digits, '.', '_' or '-'")
		os.Exit(2)
	}

	key, err := auth.NewKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, "apikey:", err)
		os.Exit(1)
	}
	hash := auth.HashKey(key)
	digest := hex.EncodeToString(hash[:])

	fmt.Printf("key (shown once, give it to the caller):\n  %s\n\n", key)
	fmt.Printf("to allow it from config, add to AUTH_KEYS (comma-separated):\n  %s=%s\n", *name, digest)
	// module:postgres begin
	fmt.Printf("\nor, to allow it from the database:\n  INSERT INTO api_keys (name, key_hash) VALUES ('%s', '\\x%s');\n", *name, digest)
	// module:postgres end
}
