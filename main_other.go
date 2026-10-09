//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "StorageTrashCleaner faqat Windows uchun mo'ljallangan.")
	os.Exit(1)
}
