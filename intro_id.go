package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// IntroID is a string that represents the file number of an introduction
// 1234-2020 for Introduction or res-1234-2020 for Resolution
type IntroID string

// File returns the File number of the introduction
// This is the format used upstream in the Legistar API
func (i IntroID) File() string {
	if strings.HasPrefix(string(i), "res-") {
		return "Res " + strings.TrimPrefix(string(i), "res-")
	}
	return "Int " + string(i)
}

func (i IntroID) Type() string {
	if strings.HasPrefix(string(i), "res-") {
		return "Resolution"
	}
	return "Introduction"
}

// FileNumber returns the File prefix as a number (without the session year)
func (i IntroID) FileNumber() int {
	c := strings.TrimPrefix(string(i), "res-")
	s, _, ok := strings.Cut(c, "-")
	n, err := strconv.Atoi(s)
	if err != nil || !ok {
		return 0
	}
	return n
}

// FileYear returns the session year of the legislation or resolution
func (i IntroID) FileYear() int {
	c := strings.TrimPrefix(string(i), "res-")
	_, y, _ := strings.Cut(c, "-")
	year, _ := strconv.Atoi(y)
	return year
}

func ParseFile(f string) (IntroID, error) {
	var i, prefix IntroID
	switch {
	case strings.HasPrefix(f, "Res "):
		prefix = "res-"
		i = IntroID(strings.TrimPrefix(f, "Res "))
	case strings.HasPrefix(f, "Int "):
		prefix = ""
		i = IntroID(strings.TrimPrefix(f, "Int "))
	default:
		return "", fmt.Errorf("invalid file number %q", f)
	}

	// some older entries have "Int 0349-1998-A"
	if strings.Count(string(i), "-") == 2 {
		i = IntroID(strings.Join(strings.Split(string(i), "-")[:2], "-"))
	}

	if !IsValidFileNumber(string(i)) {
		return "", fmt.Errorf("invalid file number %q", f)
	}
	return prefix + i, nil
}

func ParseIntroID(f string) (IntroID, error) {
	if !IsValidFileNumber(strings.TrimPrefix(f, "res-")) {
		return "", fmt.Errorf("invalid IntroID %q", f)
	}
	return IntroID(f), nil
}
func IsValidIntroID(s string) bool {
	_, err := ParseIntroID(s)
	if err != nil {
		return false
	}
	return true
}

// IsValidFileNumber matches 0123-2020
func IsValidFileNumber(f string) bool {
	if ok, _ := regexp.MatchString("^[0-9]{4}-(19|20)[9012][0-9]$", f); !ok {
		return false
	}
	n := strings.Split(f, "-")
	seq, _ := strconv.Atoi(n[0])
	if seq > 3500 || seq < 1 {
		return false
	}
	year, _ := strconv.Atoi(n[1])
	if year > time.Now().Year() || year < 1996 {
		return false
	}
	return true
}
