package testmedia

import "strconv"

func fmtInt(n int) string { return strconv.Itoa(n) }

func fmtFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
