package main

import "os"

var version = "dev"

func main() {
	_ = os.WriteFile("version-probe-was-executed", []byte(version), 0o600)
}
