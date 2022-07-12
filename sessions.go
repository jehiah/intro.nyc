package main

import "fmt"

type Session struct {
	StartYear, EndYear int // inclusive
}

func (s Session) String() string { return fmt.Sprintf("%d-%d", s.StartYear, s.EndYear) }

var Sessions = []Session{
	{2022, 2023},
	{2018, 2021},
	{2014, 2017},
	{2010, 2013},
	{2006, 2009},
	{2004, 2005},
	{2002, 2003},
	{1998, 2001},
}
var CurrentSession = Sessions[0]
