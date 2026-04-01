// keygen generates a new Ed25519 keypair for humanMCP content signing.
// Run once: go run ./cmd/keygen/
// Then set the secrets on Fly.io:
//   fly secrets set SIGNING_PRIVATE_KEY=<private> SIGNING_PUBLIC_KEY=<public>
package main

import (
	"fmt"
	"github.com/kapoost/humanmcp-go/internal/content"
	"os"
)

func main() {
	kp, err := content.GenerateKeyPair()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== humanMCP Ed25519 Signing Keys ===")
	fmt.Println()
	fmt.Println("Run this in your terminal to set Fly secrets:")
	fmt.Println()
	fmt.Printf("fly secrets set \\\n")
	fmt.Printf("  SIGNING_PRIVATE_KEY=\"%s\" \\\n", kp.PrivateKeyBase64())
	fmt.Printf("  SIGNING_PUBLIC_KEY=\"%s\"\n", kp.PublicKeyHex())
	fmt.Println()
	fmt.Println("Public key (safe to share — goes in README):")
	fmt.Printf("  %s\n", kp.PublicKeyHex())
	fmt.Println()
	fmt.Println("KEEP THE PRIVATE KEY SECRET. Never commit it.")
}
