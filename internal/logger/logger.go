package logger

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	outMu  sync.Mutex
	output io.Writer = os.Stderr
)

const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorPurple = "\033[35m"
)

type rgbColor struct {
	r, g, b uint8
}

// Curated light pastel palette
var pastelPalette = []rgbColor{
	{168, 230, 207}, // Mint Green
	{174, 214, 241}, // Sky Blue
	{215, 189, 226}, // Lavender Lilac
	{248, 196, 180}, // Peach Apricot
	{254, 243, 199}, // Buttercup Yellow
	{245, 183, 177}, // Pastel Coral Rose
	{163, 228, 215}, // Seafoam Teal
	{187, 199, 246}, // Periwinkle
	{247, 205, 220}, // Blush Pink
	{197, 235, 170}, // Pistachio Green
	{178, 235, 242}, // Ice Blue
	{225, 190, 231}, // Mauve
	{255, 224, 178}, // Honey Vanilla
	{178, 223, 219}, // Minty Cyan
	{209, 196, 233}, // Pastel Violet
	{255, 245, 157}, // Pastel Lemon
}

// ColorForCategory returns a 24-bit TrueColor ANSI escape sequence deterministically hashed from the category name.
func ColorForCategory(category string) string {
	if category == "" {
		return ""
	}
	normalized := strings.ToLower(strings.TrimSpace(category))
	var hash uint32 = 2166136261
	for i := 0; i < len(normalized); i++ {
		hash ^= uint32(normalized[i])
		hash *= 16777619
	}
	c := pastelPalette[int(hash)%len(pastelPalette)]
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", c.r, c.g, c.b)
}

// SetOutput changes the output destination for logs (useful for testing).
func SetOutput(w io.Writer) {
	outMu.Lock()
	defer outMu.Unlock()
	output = w
}

func log(level, levelColor, category, format string, args ...interface{}) {
	now := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf(format, args...)

	var catPrefix string
	if category != "" {
		catColor := ColorForCategory(category)
		catPrefix = fmt.Sprintf(" %s%s[%s]%s", colorBold, catColor, category, colorReset)
	}

	outMu.Lock()
	defer outMu.Unlock()
	fmt.Fprintf(output, "%s %s[%s]%s%s %s\n", now, levelColor, level, colorReset, catPrefix, msg)
}

func Info(category, format string, args ...interface{}) {
	log("INFO", colorGreen, category, format, args...)
}

func Warn(category, format string, args ...interface{}) {
	log("WARN", colorYellow, category, format, args...)
}

func Error(category, format string, args ...interface{}) {
	log("ERROR", colorRed, category, format, args...)
}

func Debug(category, format string, args ...interface{}) {
	log("DEBUG", colorBlue, category, format, args...)
}

func Fatal(category, format string, args ...interface{}) {
	log("FATAL", colorPurple, category, format, args...)
	os.Exit(1)
}
