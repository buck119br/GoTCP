package main

import (
	"flag"
	"fmt"
	"os"
)

var working_dir = flag.String("working_dir", ".", "Working directory.")

func main() {
	flag.Parse()

	if err := os.Chdir(*working_dir); err != nil {
		fmt.Printf("Failed to chdir to %s error: %s\n", *working_dir, err.Error())
		os.Exit(1)
	}

	if err := initNetLogger(); err != nil {
		fmt.Printf("Failed to init net logger: %s\n", err.Error())
		os.Exit(1)
	}

	manager := newServiceManager()
	manager.runServiceManager()
}
