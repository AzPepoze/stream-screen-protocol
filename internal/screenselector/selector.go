package screenselector

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ScreenSelector provides interactive screen/window selection
type ScreenSelector struct {
	backend string
}

// Screen represents a selectable screen
type Screen struct {
	Index int
	Name  string
	Desc  string
	Type  string // "monitor" or "window"
}

func NewScreenSelector(backend string) *ScreenSelector {
	return &ScreenSelector{backend: backend}
}

// ListMonitors lists available monitors based on backend
func (s *ScreenSelector) ListMonitors() ([]Screen, error) {
	switch s.backend {
	case "ddagrab":
		return s.listDDAGrabMonitors()
	case "gdigrab":
		return s.listGDIGrabMonitors()
	default:
		return nil, fmt.Errorf("unsupported backend: %s", s.backend)
	}
}

// listDDAGrabMonitors lists monitors using FFmpeg DDAgrab
func (s *ScreenSelector) listDDAGrabMonitors() ([]Screen, error) {
	var screens []Screen
	// DDAgrab uses output indices 0, 1, 2, etc.
	// Try up to 10 displays
	for i := 0; i < 10; i++ {
		screens = append(screens, Screen{
			Index: i,
			Name:  fmt.Sprintf("Display %d", i),
			Desc:  fmt.Sprintf("Monitor #%d", i),
			Type:  "monitor",
		})
	}
	return screens, nil
}

// listGDIGrabMonitors lists monitors using FFmpeg GDIGrab
func (s *ScreenSelector) listGDIGrabMonitors() ([]Screen, error) {
	// GDIGrab uses "desktop" for primary
	screens := []Screen{
		{
			Index: 0,
			Name:  "desktop",
			Desc:  "Primary Desktop",
			Type:  "monitor",
		},
	}
	return screens, nil
}

// ListWindows lists open application windows
func (s *ScreenSelector) ListWindows() ([]Screen, error) {
	var windows []Screen

	// Use PowerShell to get processes with window titles
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		`Get-Process | Where-Object {$_.MainWindowTitle -ne ''} | Select-Object -Property Name, MainWindowTitle | ConvertTo-Csv -NoTypeInformation | Select-Object -Skip 1`)
	output, err := cmd.Output()
	if err != nil {
		// Fallback: return empty list
		return windows, nil
	}

	lines := strings.Split(string(output), "\n")
	idx := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse CSV output
		parts := strings.Split(line, ",")
		if len(parts) >= 2 {
			name := strings.Trim(parts[0], "\"")
			title := strings.Trim(parts[1], "\"")

			// Skip empty or system processes
			if name == "" || title == "" {
				continue
			}

			// Skip system processes
			if isSystemProcess(name) {
				continue
			}

			windows = append(windows, Screen{
				Index: idx,
				Name:  title,
				Desc:  fmt.Sprintf("Application: %s", name),
				Type:  "window",
			})
			idx++

			// Limit to 50 windows
			if idx >= 50 {
				break
			}
		}
	}

	return windows, nil
}

// isSystemProcess checks if a process should be hidden from the list
func isSystemProcess(name string) bool {
	systemProcs := map[string]bool{
		"System":          true,
		"Idle":            true,
		"svchost.exe":     true,
		"csrss.exe":       true,
		"wininit.exe":     true,
		"conhost.exe":     true,
		"dwm.exe":         true,
		"explorer.exe":    true,
		"chrome.exe":      true,
		"firefox.exe":     true,
		"notepad.exe":     true,
	}
	return systemProcs[name]
}

// SelectScreen presents interactive screen/window selection menu
func (s *ScreenSelector) SelectScreen() (Screen, error) {
	for {
		fmt.Println("\n╔════════════════════════════════════════╗")
		fmt.Println("║  Screen Share                         ║")
		fmt.Println("╚════════════════════════════════════════╝")
		fmt.Println("\n[1] Share Monitor/Screen")
		fmt.Println("[2] Share Application Window")
		fmt.Println("[0] Exit")
		fmt.Print("\nChoose option: ")

		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch input {
		case "1":
			return s.selectMonitor()
		case "2":
			return s.selectWindow()
		case "0":
			return Screen{}, fmt.Errorf("cancelled")
		default:
			fmt.Println("Invalid option, try again")
		}
	}
}

// selectMonitor presents monitor selection menu
func (s *ScreenSelector) selectMonitor() (Screen, error) {
	monitors, err := s.ListMonitors()
	if err != nil {
		return Screen{}, err
	}

	if len(monitors) == 0 {
		return Screen{}, fmt.Errorf("no monitors found")
	}

	fmt.Println("\n📺 Available Monitors:")
	fmt.Println("──────────────────────")
	for _, mon := range monitors {
		fmt.Printf("[%d] %s - %s\n", mon.Index, mon.Name, mon.Desc)
	}

	fmt.Print("\nSelect monitor: ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	idx, err := strconv.Atoi(input)
	if err != nil {
		return Screen{}, fmt.Errorf("invalid input: %s", input)
	}

	for _, mon := range monitors {
		if mon.Index == idx {
			fmt.Printf("✓ Selected: %s\n", mon.Name)
			return mon, nil
		}
	}

	return Screen{}, fmt.Errorf("monitor %d not found", idx)
}

// selectWindow presents window selection menu
func (s *ScreenSelector) selectWindow() (Screen, error) {
	windows, err := s.ListWindows()
	if err != nil {
		return Screen{}, err
	}

	if len(windows) == 0 {
		fmt.Println("No application windows found")
		return Screen{}, fmt.Errorf("no windows available")
	}

	fmt.Println("\n🪟 Available Application Windows:")
	fmt.Println("────────────────────────────────")
	for _, win := range windows {
		fmt.Printf("[%d] %s\n", win.Index, win.Name)
	}

	fmt.Print("\nSelect application: ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	idx, err := strconv.Atoi(input)
	if err != nil {
		return Screen{}, fmt.Errorf("invalid input: %s", input)
	}

	for _, win := range windows {
		if win.Index == idx {
			fmt.Printf("✓ Selected: %s\n", win.Name)
			return win, nil
		}
	}

	return Screen{}, fmt.Errorf("application %d not found", idx)
}

// VerifyFFmpeg checks if FFmpeg is installed
func VerifyFFmpeg() error {
	cmd := exec.Command("ffmpeg", "-version")
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("ffmpeg not found: install it from https://ffmpeg.org/download.html")
	}
	return nil
}

// InteractiveSetup runs interactive setup
func InteractiveSetup(backend string) (Screen, error) {
	if err := VerifyFFmpeg(); err != nil {
		return Screen{}, err
	}

	selector := NewScreenSelector(backend)
	screen, err := selector.SelectScreen()
	if err != nil {
		return Screen{}, err
	}

	return screen, nil
}
