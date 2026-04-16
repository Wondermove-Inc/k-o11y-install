package logger

import (
	"fmt"
	"os"
)

const (
	colorRed    = "\033[0;31m"
	colorGreen  = "\033[0;32m"
	colorYellow = "\033[1;33m"
	colorBlue   = "\033[0;34m"
	colorReset  = "\033[0m"
)

func Info(format string, args ...interface{})  { fmt.Printf(colorBlue+"[INFO]"+colorReset+" "+format+"\n", args...) }
func OK(format string, args ...interface{})    { fmt.Printf(colorGreen+"[OK]"+colorReset+" "+format+"\n", args...) }
func Warn(format string, args ...interface{})  { fmt.Printf(colorYellow+"[WARN]"+colorReset+" "+format+"\n", args...) }
func Error(format string, args ...interface{}) { fmt.Fprintf(os.Stderr, colorRed+"[ERROR]"+colorReset+" "+format+"\n", args...) }
