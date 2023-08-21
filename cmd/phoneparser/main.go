package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/nyaruka/phonenumbers"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Println("usage: phoneparser [number] [two letter coutry]")
		os.Exit(1)
	}

	num, err := phonenumbers.Parse(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Printf("Error parsing number: %s\n", err)
	}

	metadata, err := phonenumbers.Parse(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Printf("error parsing phone", err.Error())
	}
	fmt.Printf("            E164: %s\n", phonenumbers.Format(num, phonenumbers.E164))
	fmt.Printf("National Dialing: %s\n", phonenumbers.Format(num, phonenumbers.NATIONAL))
	fmt.Printf("        National: %d\n", num.NationalNumber)
	fmt.Printf("         IsValid: %s\n", strconv.FormatBool(phonenumbers.IsValidNumber(metadata)))

}
