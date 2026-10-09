// Command analysisfpcalc is a platform-native stand-in for the managed fpcalc
// executable used by source-analysis integration tests.
package main

import (
	"fmt"
	"os"
)

const (
	release = "1.6.1"
	// This is a valid compressed Chromaprint fingerprint used by fpcalc tests.
	fingerprint = "AQAAEwkjrUmSJQpUHflR9mjSJMdZpcO_Imdw9dCO9Clu4_wQPvhCB01w6xAtXNcAp5RASgDBhDSCGGIAcwA"
)

func main() {
	for _, argument := range os.Args[1:] {
		if argument == "-version" {
			fmt.Printf("fpcalc version %s\n", release)
			return
		}
	}
	fmt.Printf("{\"duration\":1.5,\"fingerprint\":%q}\n", fingerprint)
}
