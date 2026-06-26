package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Simple Calculator - Add two numbers")
		fmt.Println("Usage: go run main.go <number1> <number2>")
		return
	}

	num1, err1 := strconv.ParseFloat(os.Args[1], 64)
	if err1 != nil {
		fmt.Printf("Error: '%s' is not a valid number\n", os.Args[1])
		return
	}

	num2, err2 := strconv.ParseFloat(os.Args[2], 64)
	if err2 != nil {
		fmt.Printf("Error: '%s' is not a valid number\n", os.Args[2])
		return
	}

	sum := num1 + num2
	fmt.Printf("%.2f + %.2f = %.2f\n", num1, num2, sum)
}
