package main

import (
    "flag"
    "fmt"
    "os"
)

func main() {
    if len(os.Args) < 2 {
        printUsage()
        os.Exit(1)
    }

    command := os.Args[1]
    
    switch command {
    case "encode":
        runEncode(os.Args[2:])
    case "decode":
        runDecode(os.Args[2:])
    case "test":
        runSanityCheck()
    default:
        fmt.Printf("Unknown command: %s\n", command)
        printUsage()
        os.Exit(1)
    }
}

func printUsage() {
    fmt.Println("GAP Engine CLI v1.1")
    fmt.Println("Usage:")
    fmt.Println("  gap-engine encode -i input.jpg -o output.gap [-s 0.1] [-t 0.5]")
    fmt.Println("  gap-engine decode -i input.gap -o output.png")
}

func runDecode(args []string) {
    fs := flag.NewFlagSet("decode", flag.ExitOnError)
    inputPtr := fs.String("i", "", "Input gap file path")
    outputPtr := fs.String("o", "", "Output png file path")
    
    fs.Parse(args)
    
    if *inputPtr == "" || *outputPtr == "" {
        fmt.Println("Error: -i and -o are required")
        fs.PrintDefaults()
        os.Exit(1)
    }
    
    err := DecodeImage(*inputPtr, *outputPtr)
    if err != nil {
        fmt.Printf("Decoding failed: %v\n", err)
        os.Exit(1)
    }
}

func runEncode(args []string) {
    fs := flag.NewFlagSet("encode", flag.ExitOnError)
    inputPtr := fs.String("i", "", "Input image path")
    outputPtr := fs.String("o", "", "Output gap file path")
    sPtr := fs.Float64("s", 0.1, "PLTM Decay (s)")
    tPtr := fs.Float64("t", 0.5, "Threshold")
    
    fs.Parse(args)
    
    if *inputPtr == "" || *outputPtr == "" {
        fmt.Println("Error: -i and -o are required")
        fs.PrintDefaults()
        os.Exit(1)
    }
    
    err := EncodeImage(*inputPtr, *outputPtr, float32(*sPtr), float32(*tPtr))
    if err != nil {
        fmt.Printf("Encoding failed: %v\n", err)
        os.Exit(1)
    }
    
    fmt.Println("Success.")
}

func runSanityCheck() {
	fmt.Println("Running GAP Engine Sanity Check...")

	// Test Range Coder Bridge
	input := []byte("Hello GAP! This is a test of the Range Coder bridge.")
	compressed := GapCompressData(input)
	if compressed == nil {
		fmt.Println("FAILED: GapCompressData returned nil")
		os.Exit(1)
	}

	decompressed := GapDecompressData(compressed, len(input))
	if string(decompressed) != string(input) {
		fmt.Printf("FAILED: Decompression mismatch.\nExpected: %s\nGot: %s\n", string(input), string(decompressed))
		os.Exit(1)
	}

	fmt.Println("Range Coder Bridge: OK")
	fmt.Println("Sanity Check PASSED.")
}
