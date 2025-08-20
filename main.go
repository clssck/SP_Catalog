package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type appState int

const (
	stateForm appState = iota
	stateBrowser
	stateScanning
	stateDone
	stateHelp
)

type formModel struct {
	root   textinput.Model // required
	outDir textinput.Model // optional (defaults to $HOME/spcatalog)
	ext    textinput.Model // optional: ".pdf,.docx"
	hashOn bool

	focus int // 0=root, 1=outDir, 2=ext
	err   string

	// Autocomplete state
	completions        []string
	completionIndex    int
	showingCompletions bool

	// Path validation state
	rootPathValid   int // 0=unknown, 1=valid, 2=partial, 3=invalid
	outDirPathValid int // 0=unknown, 1=valid, 2=partial, 3=invalid

	// Recent paths state
	recentPaths []string
}

type browserModel struct {
	currentPath string
	entries     []os.DirEntry
	selected    int
	err         string
}

type helpModel struct {
	previousState appState
}

type model struct {
	state      appState
	form       formModel
	browser    browserModel
	help       helpModel
	spin       spinner.Model
	start      time.Time
	stats      stats
	dbPath     string
	err        error
	windowSize tea.WindowSizeMsg
}

type stats struct {
	files          int64
	folders        int64
	last           string
	estimatedTotal int64   // Estimated total files to process
	progress       float64 // Progress percentage (0-100)
}

// Configuration for persistent settings
type appConfig struct {
	RecentPaths     []string `json:"recent_paths"`
	MaxRecent       int      `json:"max_recent"`
	LastRootPath    string   `json:"last_root_path"`
	LastOutputDir   string   `json:"last_output_dir"`
	LastExtFilter   string   `json:"last_ext_filter"`
	LastHashSetting bool     `json:"last_hash_setting"`
}

type progressMsg stats
type estimationMsg struct{ totalFiles int64 }
type doneMsg struct{ err error }

var (
	lbl = lipgloss.NewStyle().Faint(true)
	val = lipgloss.NewStyle().Bold(true)
	ok  = lipgloss.NewStyle().Foreground(lipgloss.Color("#5fff87")).Bold(true)
	bad = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5f5f")).Bold(true)
	acc = lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7"))
)

func main() {
	home, _ := os.UserHomeDir()
	defaultOut := filepath.Join(home, "spcatalog")

	// Load configuration and recent paths
	config := loadConfig()

	root := textinput.New()
	root.Placeholder = "/Users/you/OneDrive - Company/SharePoint"
	root.Prompt = "Root path: "
	// Use last root path if available, otherwise empty
	if config.LastRootPath != "" {
		root.SetValue(config.LastRootPath)
	}
	root.Focus()

	outDir := textinput.New()
	outDir.Prompt = "Output dir (optional): "
	// Use last output dir if available, otherwise default
	if config.LastOutputDir != "" {
		outDir.SetValue(config.LastOutputDir)
	} else {
		outDir.SetValue(defaultOut)
	}

	ext := textinput.New()
	ext.Prompt = "Ext filter (optional, e.g. .pdf,.docx): "
	// Use last extension filter if available
	if config.LastExtFilter != "" {
		ext.SetValue(config.LastExtFilter)
	}

	s := spinner.New()
	s.Spinner = spinner.Dot

	m := model{
		state: stateForm,
		form: formModel{
			root:        root,
			outDir:      outDir,
			ext:         ext,
			hashOn:      config.LastHashSetting, // Use saved hash setting
			focus:       0,
			recentPaths: config.RecentPaths,
		},
		spin: s,
	}

	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
}

// INIT
func (m model) Init() tea.Cmd { return nil }

// UPDATE
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.state {
	case stateForm:
		return m.updateForm(msg)
	case stateBrowser:
		return m.updateBrowser(msg)
	case stateScanning:
		return m.updateScan(msg)
	case stateDone:
		if _, ok := msg.(tea.KeyMsg); ok {
			return m, tea.Quit
		}
		return m, nil
	case stateHelp:
		return m.updateHelp(msg)
	default:
		return m, nil
	}
}

func (m model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab":
			// Tab completion for path fields
			if m.form.focus == 0 || m.form.focus == 1 { // root or outDir
				return m.handleTabCompletion()
			}
			// Otherwise, move to next field
			m.form.focus = (m.form.focus + 1) % 3
			m.setFocus()
		case "down":
			if m.form.showingCompletions && len(m.form.completions) > 0 {
				m.form.completionIndex = (m.form.completionIndex + 1) % len(m.form.completions)
				return m, nil
			}
			m.form.focus = (m.form.focus + 1) % 3
			m.setFocus()
		case "shift+tab", "up":
			if m.form.showingCompletions && len(m.form.completions) > 0 {
				m.form.completionIndex = (m.form.completionIndex + len(m.form.completions) - 1) % len(m.form.completions)
				return m, nil
			}
			m.form.focus = (m.form.focus + 2) % 3
			m.setFocus()
		case " ":
			// toggle hash
			m.form.hashOn = !m.form.hashOn
		case "ctrl+b":
			// open directory browser starting from current path context
			startPath := m.getBrowserStartPath()
			m.state = stateBrowser
			m.browser = browserModel{currentPath: startPath}
			return m, m.loadBrowserEntries()
		case "enter":
			// If showing completions, select the current completion
			if m.form.showingCompletions && len(m.form.completions) > 0 {
				return m.selectCompletion()
			}
			// Otherwise, submit
			root := strings.TrimSpace(m.form.root.Value())
			if root == "" {
				m.form.err = "Root is required."
				return m, nil
			}
			if _, err := os.Stat(root); err != nil {
				m.form.err = "Root not accessible."
				return m, nil
			}
			outDir := strings.TrimSpace(m.form.outDir.Value())
			if outDir == "" {
				home, _ := os.UserHomeDir()
				outDir = filepath.Join(home, "spcatalog")
			}
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				m.form.err = "Failed to create output dir."
				return m, nil
			}
			dbPath := filepath.Join(outDir, "catalog.db")
			extSet := parseExtSet(strings.TrimSpace(m.form.ext.Value()))

			// Save all preferences before starting scan
			config := &appConfig{
				RecentPaths:     addToRecentPaths(m.form.recentPaths, root, 9),
				MaxRecent:       9,
				LastRootPath:    root,
				LastOutputDir:   outDir,
				LastExtFilter:   strings.TrimSpace(m.form.ext.Value()),
				LastHashSetting: m.form.hashOn,
			}
			saveConfig(config) // Ignore errors for config saving

			m.dbPath = dbPath
			m.state = stateScanning
			m.start = time.Now()
			return m, tea.Batch(m.spin.Tick, runScan(root, dbPath, extSet, m.form.hashOn))
		case "esc":
			// Clear completions if showing, otherwise quit
			if m.form.showingCompletions {
				m.form.showingCompletions = false
				m.form.completions = nil
				return m, nil
			}
			return m, tea.Quit
		case "q", "ctrl+c":
			return m, tea.Quit
		case "?", "h", "F1":
			// Show help
			m.help.previousState = m.state
			m.state = stateHelp
			return m, nil
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			// Quick select recent path
			if len(m.form.recentPaths) > 0 {
				index := int(msg.String()[0] - '1') // Convert '1' to 0, '2' to 1, etc.
				if index < len(m.form.recentPaths) {
					m.form.root.SetValue(m.form.recentPaths[index])
					m.form.root.CursorEnd()
					m.form.rootPathValid = validatePath(m.form.recentPaths[index])
				}
			}
			return m, nil
		}
	case tea.WindowSizeMsg:
		m.windowSize = msg
	}

	// Update the text input and validate path in real-time
	switch m.form.focus {
	case 0:
		m.form.root, cmd = m.form.root.Update(msg)
		m.form.rootPathValid = validatePath(m.form.root.Value())
	case 1:
		m.form.outDir, cmd = m.form.outDir.Update(msg)
		m.form.outDirPathValid = validatePath(m.form.outDir.Value())
	case 2:
		m.form.ext, cmd = m.form.ext.Update(msg)
	}
	return m, cmd
}

func (m *model) setFocus() {
	m.form.root.Blur()
	m.form.outDir.Blur()
	m.form.ext.Blur()

	// Clear completions when changing focus
	m.form.showingCompletions = false
	m.form.completions = nil

	switch m.form.focus {
	case 0:
		m.form.root.Focus()
	case 1:
		m.form.outDir.Focus()
	case 2:
		m.form.ext.Focus()
	}
}

// Handle tab completion for path fields
func (m model) handleTabCompletion() (tea.Model, tea.Cmd) {
	var currentPath string
	if m.form.focus == 0 {
		currentPath = m.form.root.Value()
	} else {
		currentPath = m.form.outDir.Value()
	}

	// Get path completions
	completions := getPathCompletions(currentPath)

	// If we got only one completion, auto-complete it immediately
	if len(completions) == 1 {
		if m.form.focus == 0 {
			m.form.root.SetValue(completions[0])
			m.form.root.CursorEnd() // Move cursor to end
		} else {
			m.form.outDir.SetValue(completions[0])
			m.form.outDir.CursorEnd() // Move cursor to end
		}
		return m, nil
	}

	if len(completions) == 0 {
		return m, nil
	}

	m.form.completions = completions
	m.form.completionIndex = 0
	m.form.showingCompletions = true

	return m, nil
}

// Select the current completion
func (m model) selectCompletion() (tea.Model, tea.Cmd) {
	if !m.form.showingCompletions || len(m.form.completions) == 0 {
		return m, nil
	}

	completion := m.form.completions[m.form.completionIndex]

	if m.form.focus == 0 {
		m.form.root.SetValue(completion)
		m.form.root.CursorEnd() // Move cursor to end
	} else {
		m.form.outDir.SetValue(completion)
		m.form.outDir.CursorEnd() // Move cursor to end
	}

	m.form.showingCompletions = false
	m.form.completions = nil

	return m, nil
}

// Get path completions for a given path
func getPathCompletions(path string) []string {
	if path == "" {
		// Start with home directory
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		return getPathCompletions(home)
	}

	path = strings.TrimSpace(path)

	// If the path exists as a directory, list its contents
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil
		}

		var completions []string
		for _, entry := range entries {
			if !entry.IsDir() {
				continue // Only directories
			}

			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue // Skip hidden directories
			}

			fullPath := filepath.Join(path, name)
			completions = append(completions, fullPath)
		}

		return completions
	}

	// Path doesn't exist - treat as partial path
	dir := filepath.Dir(path)
	prefix := filepath.Base(path)

	// Make sure the parent directory exists
	if _, err := os.Stat(dir); err != nil {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var completions []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue // Only directories
		}

		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue // Skip hidden directories
		}

		// Case-insensitive prefix matching
		if prefix == "" || strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			fullPath := filepath.Join(dir, name)
			completions = append(completions, fullPath)
		}
	}

	return completions
}

// Validate a path and return status: 1=valid, 2=partial, 3=invalid
func validatePath(path string) int {
	if path == "" {
		return 0 // unknown/empty
	}

	path = strings.TrimSpace(path)

	// Check if path exists as directory
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return 1 // valid directory
	}

	// Check if parent directory exists (partial path)
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); err == nil {
		return 2 // partial path (parent exists)
	}

	return 3 // invalid
}

// Update path validation for the focused field
func (m *model) updatePathValidation() {
	if m.form.focus == 0 {
		m.form.rootPathValid = validatePath(m.form.root.Value())
	} else if m.form.focus == 1 {
		m.form.outDirPathValid = validatePath(m.form.outDir.Value())
	}
}

// Get visual indicator for path validation status
func getPathValidationIndicator(status int) string {
	switch status {
	case 0: // unknown/empty
		return ""
	case 1: // valid
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e")).Render("✓")
	case 2: // partial
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Render("⚠")
	case 3: // invalid
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444")).Render("✗")
	default:
		return ""
	}
}

// Get the appropriate starting path for the browser based on current context
func (m model) getBrowserStartPath() string {
	// Try to use the current root path
	if currentRoot := strings.TrimSpace(m.form.root.Value()); currentRoot != "" {
		// If it's a valid directory, start there
		if info, err := os.Stat(currentRoot); err == nil && info.IsDir() {
			return currentRoot
		}
		// If it's a file or partial path, start from its directory
		if dir := filepath.Dir(currentRoot); dir != "." {
			if _, err := os.Stat(dir); err == nil {
				return dir
			}
		}
	}

	// Try OneDrive directories
	home, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}

	// Common OneDrive locations
	onedrivePaths := []string{
		filepath.Join(home, "OneDrive"),
		filepath.Join(home, "OneDrive - Personal"),
		filepath.Join(home, "Library", "CloudStorage", "OneDrive-Personal"),
		filepath.Join(home, "Library", "CloudStorage"),
	}

	for _, path := range onedrivePaths {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
	}

	// Fallback to home directory
	return home
}

func (m model) updateBrowser(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case browserLoadedMsg:
		m.browser.entries = msg.entries
		m.browser.selected = 0
		m.browser.err = ""
		return m, nil
	case browserErrorMsg:
		m.browser.err = msg.err.Error()
		return m, nil
	case tea.WindowSizeMsg:
		m.windowSize = msg
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			// return to form
			m.state = stateForm
			return m, nil
		case "?", "h", "F1":
			// Show help
			m.help.previousState = m.state
			m.state = stateHelp
			return m, nil
		case "up", "k":
			if m.browser.selected > 0 {
				m.browser.selected--
			}
		case "down", "j":
			if m.browser.selected < len(m.browser.entries)-1 {
				m.browser.selected++
			}
		case "enter":
			if len(m.browser.entries) > 0 {
				entry := m.browser.entries[m.browser.selected]
				if entry.IsDir() {
					if entry.Name() == ".." {
						m.browser.currentPath = filepath.Dir(m.browser.currentPath)
					} else {
						m.browser.currentPath = filepath.Join(m.browser.currentPath, entry.Name())
					}
					return m, m.loadBrowserEntries()
				} else {
					// Select this directory and return to form
					m.form.root.SetValue(m.browser.currentPath)
					m.state = stateForm
					return m, nil
				}
			}
		case " ":
			// Select current directory and return to form
			m.form.root.SetValue(m.browser.currentPath)
			m.state = stateForm
			return m, nil
		}
	}
	return m, nil
}

func (m model) updateHelp(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.windowSize = msg
		return m, nil
	case tea.KeyMsg:
		// Any key exits help and returns to previous state
		m.state = m.help.previousState
		return m, nil
	}
	return m, nil
}

func (m model) updateScan(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case estimationMsg:
		m.stats.estimatedTotal = msg.totalFiles
		return m, nil
	case progressMsg:
		m.stats.files = msg.files
		m.stats.folders = msg.folders
		m.stats.last = msg.last
		m.stats.estimatedTotal = msg.estimatedTotal
		// Calculate progress percentage
		if m.stats.estimatedTotal > 0 {
			m.stats.progress = float64(m.stats.files) / float64(m.stats.estimatedTotal) * 100
			if m.stats.progress > 100 {
				m.stats.progress = 100
			}
		}
		return m, nil
	case doneMsg:
		m.state = stateDone
		m.err = msg.err
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.windowSize = msg
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			// just quit; WAL ensures db integrity
			return m, tea.Quit
		case "?", "h", "F1":
			// Show help
			m.help.previousState = m.state
			m.state = stateHelp
			return m, nil
		}
	}
	return m, nil
}

// Helper methods
func (m model) loadBrowserEntries() tea.Cmd {
	return func() tea.Msg {
		entries, err := os.ReadDir(m.browser.currentPath)
		if err != nil {
			return browserErrorMsg{err: err}
		}

		// Add parent directory entry if not at root
		var allEntries []os.DirEntry
		if m.browser.currentPath != "/" && m.browser.currentPath != filepath.VolumeName(m.browser.currentPath) {
			// Create a fake ".." entry
			allEntries = append(allEntries, &parentDirEntry{})
		}

		// Filter to only show directories
		for _, entry := range entries {
			if entry.IsDir() {
				allEntries = append(allEntries, entry)
			}
		}

		return browserLoadedMsg{entries: allEntries}
	}
}

type browserErrorMsg struct{ err error }
type browserLoadedMsg struct{ entries []os.DirEntry }

// Fake DirEntry for parent directory
type parentDirEntry struct{}

func (p *parentDirEntry) Name() string               { return ".." }
func (p *parentDirEntry) IsDir() bool                { return true }
func (p *parentDirEntry) Type() os.FileMode          { return os.ModeDir }
func (p *parentDirEntry) Info() (os.FileInfo, error) { return nil, nil }

// VIEW
func (m model) View() string {
	switch m.state {
	case stateForm:
		return m.viewForm()
	case stateBrowser:
		return m.viewBrowser()
	case stateScanning:
		return m.viewScan()
	case stateDone:
		return m.viewDone()
	case stateHelp:
		return m.viewHelp()
	default:
		return ""
	}
}

func (m model) viewForm() string {
	var b strings.Builder

	// Create a beautiful full-screen background with decorative borders
	width := m.getWidth()

	// Main content area with padding
	contentWidth := width - 6 // Leave space for borders and padding

	// Beautiful header with gradient colors and decorative border
	headerBox := lipgloss.NewStyle().
		Width(contentWidth).
		Align(lipgloss.Center).
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("#7c3aed")).
		Background(lipgloss.Color("#1e1b4b")).
		Padding(1, 2).
		MarginBottom(1)

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#a78bfa")).
		Bold(true).
		Render("🗂  SharePoint → SQLite Catalog")

	subtitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#c4b5fd")).
		Italic(true).
		Render("✨ Fast • Beautiful • Functional ✨")

	headerContent := lipgloss.JoinVertical(lipgloss.Center, title, "", subtitle)
	header := headerBox.Render(headerContent)

	fmt.Fprintf(&b, "%s\n", header)

	// Create a beautiful form container with decorative borders
	formBox := lipgloss.NewStyle().
		Width(contentWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#4c1d95")).
		Background(lipgloss.Color("#0f0f23")).
		Padding(2, 3).
		MarginBottom(1)

	var formContent strings.Builder

	// Style the input fields with beautiful colors and validation indicators
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a78bfa")).Bold(true)

	// Root path with validation indicator
	rootIndicator := getPathValidationIndicator(m.form.rootPathValid)
	fmt.Fprintf(&formContent, "%s%s %s\n", labelStyle.Render(m.form.root.Prompt), m.form.root.View(), rootIndicator)

	// Output dir with validation indicator
	outDirIndicator := getPathValidationIndicator(m.form.outDirPathValid)
	fmt.Fprintf(&formContent, "%s%s %s\n", labelStyle.Render(m.form.outDir.Prompt), m.form.outDir.View(), outDirIndicator)

	// Extension field (no validation needed)
	fmt.Fprintf(&formContent, "%s%s\n", labelStyle.Render(m.form.ext.Prompt), m.form.ext.View())

	// Hash toggle with beautiful styling
	hashMark := "off"
	hashColor := lipgloss.Color("#ef4444") // Red for off
	if m.form.hashOn {
		hashMark = "on"
		hashColor = lipgloss.Color("#22c55e") // Green for on
	}
	fmt.Fprintf(&formContent, "%s %s  %s\n",
		labelStyle.Render("Hash:"),
		lipgloss.NewStyle().Foreground(hashColor).Bold(true).Render(hashMark),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#c4b5fd")).Render("(SPACE toggles)"))

	// Render the form box
	form := formBox.Render(formContent.String())
	fmt.Fprintf(&b, "%s\n", form)

	// Error styling with beautiful container
	if m.form.err != "" {
		errorBox := lipgloss.NewStyle().
			Width(contentWidth).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#dc2626")).
			Background(lipgloss.Color("#450a0a")).
			Padding(1, 2)

		errorContent := lipgloss.NewStyle().Foreground(lipgloss.Color("#fca5a5")).Bold(true).Render("⚠ " + m.form.err)
		fmt.Fprintf(&b, "%s\n", errorBox.Render(errorContent))
	}

	// Show recent paths if available and not showing completions
	if !m.form.showingCompletions && len(m.form.recentPaths) > 0 {
		recentBox := lipgloss.NewStyle().
			Width(contentWidth).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#059669")).
			Background(lipgloss.Color("#022c22")).
			Padding(1, 2)

		var recentContent strings.Builder
		fmt.Fprintf(&recentContent, "%s\n\n",
			lipgloss.NewStyle().Foreground(lipgloss.Color("#6ee7b7")).Bold(true).Render("⏱  Recent Paths (press 1-9 to select)"))

		for i, recentPath := range m.form.recentPaths {
			if i >= 9 {
				break // Only show first 9 for number shortcuts
			}

			// Number shortcut with beautiful styling
			numberStyle := lipgloss.NewStyle().
				Background(lipgloss.Color("#7c3aed")).
				Foreground(lipgloss.Color("#f1f5f9")).
				Bold(true).
				Padding(0, 1).
				MarginRight(1)
			numberKey := numberStyle.Render(fmt.Sprintf("%d", i+1))

			// Path display (show just the last two directory components for readability)
			displayPath := recentPath
			pathParts := strings.Split(filepath.Clean(recentPath), string(filepath.Separator))
			if len(pathParts) > 2 {
				displayPath = "..." + string(filepath.Separator) + strings.Join(pathParts[len(pathParts)-2:], string(filepath.Separator))
			}

			// Validation indicator for recent path
			validation := getPathValidationIndicator(validatePath(recentPath))

			fmt.Fprintf(&recentContent, "%s %s %s\n",
				numberKey,
				lipgloss.NewStyle().Foreground(lipgloss.Color("#86efac")).Render(displayPath),
				validation)
		}

		fmt.Fprintf(&b, "%s\n", recentBox.Render(recentContent.String()))
	}

	// Show autocomplete suggestions if available
	if m.form.showingCompletions && len(m.form.completions) > 0 {
		suggestionBox := lipgloss.NewStyle().
			Width(contentWidth).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#0891b2")).
			Background(lipgloss.Color("#0c4a6e")).
			Padding(1, 2)

		var suggestionContent strings.Builder
		fmt.Fprintf(&suggestionContent, "%s\n\n",
			lipgloss.NewStyle().Foreground(lipgloss.Color("#7dd3fc")).Bold(true).Render("📁 Path Suggestions"))

		maxShow := 5 // Show up to 5 suggestions
		for i, completion := range m.form.completions {
			if i >= maxShow {
				fmt.Fprintf(&suggestionContent, "  %s\n",
					lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render(fmt.Sprintf("... and %d more", len(m.form.completions)-maxShow)))
				break
			}

			prefix := "  "
			style := lipgloss.NewStyle().Foreground(lipgloss.Color("#67e8f9"))
			if i == m.form.completionIndex {
				prefix = lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24")).Render("▸ ")
				style = style.Bold(true)
			}

			// Show just the directory name, not the full path
			displayName := filepath.Base(completion)
			fmt.Fprintf(&suggestionContent, "%s%s\n", prefix, style.Render(displayName))
		}
		fmt.Fprintf(&suggestionContent, "\n%s\n",
			lipgloss.NewStyle().Foreground(lipgloss.Color("#bae6fd")).Render("↑/↓ navigate • Enter select • Tab/Escape cancel"))

		fmt.Fprintf(&b, "%s\n", suggestionBox.Render(suggestionContent.String()))
	}

	// Beautiful footer with keyboard shortcuts
	footerBox := lipgloss.NewStyle().
		Width(contentWidth).
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("#7c3aed")).
		Background(lipgloss.Color("#1e1b4b")).
		Align(lipgloss.Center).
		Padding(1, 2)

	// Create beautiful keyboard shortcut buttons
	enterKey := lipgloss.NewStyle().Background(lipgloss.Color("#7c3aed")).Foreground(lipgloss.Color("#f1f5f9")).Padding(0, 1).Bold(true).Render("Enter")
	tabKey := lipgloss.NewStyle().Background(lipgloss.Color("#10b981")).Foreground(lipgloss.Color("#f1f5f9")).Padding(0, 1).Bold(true).Render("TAB")
	browseKey := lipgloss.NewStyle().Background(lipgloss.Color("#06b6d4")).Foreground(lipgloss.Color("#f1f5f9")).Padding(0, 1).Bold(true).Render("Ctrl+B")
	numberKey := lipgloss.NewStyle().Background(lipgloss.Color("#f59e0b")).Foreground(lipgloss.Color("#f1f5f9")).Padding(0, 1).Bold(true).Render("1-9")
	helpKey := lipgloss.NewStyle().Background(lipgloss.Color("#3b82f6")).Foreground(lipgloss.Color("#f1f5f9")).Padding(0, 1).Bold(true).Render("?")
	quitKey := lipgloss.NewStyle().Background(lipgloss.Color("#ef4444")).Foreground(lipgloss.Color("#f1f5f9")).Padding(0, 1).Bold(true).Render("q")

	keyHelp := fmt.Sprintf("%s start • %s complete • %s browse • %s recent • %s help • %s quit",
		enterKey, tabKey, browseKey, numberKey, helpKey, quitKey)

	footer := footerBox.Render(keyHelp)
	fmt.Fprintf(&b, "%s\n", footer)

	return b.String()
}

func (m model) viewBrowser() string {
	var b strings.Builder

	// Header with beautiful styling
	fmt.Fprintf(&b, "%s\n\n",
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7c3aed")).Render("📁 Directory Browser"))

	// Current path with responsive wrapping and beautiful colors
	pathWidth := m.getWidth() - 15 // Account for "Current: " prefix
	wrappedPath := m.wrapText(m.browser.currentPath, pathWidth)
	fmt.Fprintf(&b, "%s %s\n\n",
		lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Bold(true).Render("Current:"),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Render(wrappedPath))

	// Error handling with beautiful colors
	if m.browser.err != "" {
		errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444")).Bold(true)
		fmt.Fprintf(&b, "%s %s\n\n", errorStyle.Render("⚠ Error:"), m.browser.err)
		fmt.Fprintf(&b, "%s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render("Press ESC to go back"))
		return b.String()
	}

	// Directory listing with beautiful colors
	if len(m.browser.entries) == 0 {
		fmt.Fprintf(&b, "%s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render("No directories found"))
	} else {
		fmt.Fprintf(&b, "%s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("#f1f5f9")).Bold(true).Render("📂 Directories:"))

		// Show entries with responsive scrolling
		maxDisplay := m.getBrowserDisplayLines()
		start := 0
		if m.browser.selected >= maxDisplay {
			start = m.browser.selected - maxDisplay + 1
		}
		end := start + maxDisplay
		if end > len(m.browser.entries) {
			end = len(m.browser.entries)
		}

		for i := start; i < end; i++ {
			entry := m.browser.entries[i]
			prefix := "  "
			if i == m.browser.selected {
				prefix = lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Render("▸ ")
			}

			name := entry.Name()
			if name == ".." {
				fmt.Fprintf(&b, "%s%s\n", prefix,
					lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render("../"))
			} else {
				fmt.Fprintf(&b, "%s%s\n", prefix,
					lipgloss.NewStyle().Foreground(lipgloss.Color("#10b981")).Render(name+"/"))
			}
		}

		// Show scroll indicator if needed
		if len(m.browser.entries) > maxDisplay {
			fmt.Fprintf(&b, "\n%s\n",
				lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render(fmt.Sprintf("(%d-%d of %d)", start+1, end, len(m.browser.entries))))
		}
	}

	// Beautiful help text
	helpText := lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render("↑/↓ or j/k navigate • Enter to enter dir • Space to select • ESC to cancel")
	fmt.Fprintf(&b, "\n%s\n", helpText)

	return b.String()
}

func (m model) viewHelp() string {
	var b strings.Builder

	// Header with beautiful styling
	fmt.Fprintf(&b, "%s\n\n",
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7c3aed")).Render("❓ SharePoint Catalog - Help & Keyboard Shortcuts"))

	// General shortcuts with beautiful colors
	fmt.Fprintf(&b, "%s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("#f1f5f9")).Bold(true).Render("🔸 Global Shortcuts"))
	fmt.Fprintf(&b, "  %s %s\n",
		lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Render("?/h/F1"),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render("Show this help screen"))
	fmt.Fprintf(&b, "  %s %s\n",
		lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Render("q/ESC"),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render("Quit application (or return to previous screen)"))
	fmt.Fprintf(&b, "  %s %s\n\n",
		lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Render("Ctrl+C"),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render("Force quit"))

	// Form screen shortcuts
	fmt.Fprintf(&b, "%s\n", val.Render("🔸 Setup Form"))
	fmt.Fprintf(&b, "  %s %s\n", acc.Render("Tab/↓"), lbl.Render("Move to next field"))
	fmt.Fprintf(&b, "  %s %s\n", acc.Render("Shift+Tab/↑"), lbl.Render("Move to previous field"))
	fmt.Fprintf(&b, "  %s %s\n", acc.Render("Space"), lbl.Render("Toggle hash calculation on/off"))
	fmt.Fprintf(&b, "  %s %s\n", acc.Render("Ctrl+B"), lbl.Render("Open directory browser"))
	fmt.Fprintf(&b, "  %s %s\n\n", acc.Render("Enter"), lbl.Render("Start cataloging"))

	// Browser screen shortcuts
	fmt.Fprintf(&b, "%s\n", val.Render("🔸 Directory Browser"))
	fmt.Fprintf(&b, "  %s %s\n", acc.Render("↑/↓ or j/k"), lbl.Render("Navigate up/down"))
	fmt.Fprintf(&b, "  %s %s\n", acc.Render("Enter"), lbl.Render("Enter selected directory"))
	fmt.Fprintf(&b, "  %s %s\n", acc.Render("Space"), lbl.Render("Select current directory"))
	fmt.Fprintf(&b, "  %s %s\n\n", acc.Render("ESC"), lbl.Render("Return to setup form"))

	// Scanning screen shortcuts
	fmt.Fprintf(&b, "%s\n", val.Render("🔸 Scanning Progress"))
	fmt.Fprintf(&b, "  %s %s\n", acc.Render("q/ESC"), lbl.Render("Stop scanning (safe - database preserved)"))
	fmt.Fprintf(&b, "  %s %s\n\n", acc.Render("Ctrl+C"), lbl.Render("Force stop"))

	// Usage tips
	fmt.Fprintf(&b, "%s\n", val.Render("🔸 Usage Tips"))
	fmt.Fprintf(&b, "  • %s\n", lbl.Render("Use extension filter like: .pdf,.docx,.xlsx"))
	fmt.Fprintf(&b, "  • %s\n", lbl.Render("Hash calculation adds file integrity checking but takes longer"))
	fmt.Fprintf(&b, "  • %s\n", lbl.Render("Output database is SQLite - query with any SQLite tool"))
	fmt.Fprintf(&b, "  • %s\n", lbl.Render("Stopping scan early preserves already cataloged data"))
	fmt.Fprintf(&b, "  • %s\n\n", lbl.Render("Database uses WAL mode for performance and safety"))

	// Database schema
	fmt.Fprintf(&b, "%s\n", val.Render("🔸 Database Schema"))
	fmt.Fprintf(&b, "  %s %s\n", acc.Render("files:"), lbl.Render("abs_path, folder_path, name, ext, size, mtime_utc, mime, sha256"))
	fmt.Fprintf(&b, "  %s %s\n\n", acc.Render("folders:"), lbl.Render("path, parent_path, mtime_utc"))

	// Example queries
	fmt.Fprintf(&b, "%s\n", val.Render("🔸 Example SQLite Queries"))
	fmt.Fprintf(&b, "  %s\n", acc.Render("SELECT * FROM files WHERE ext = '.pdf';"))
	fmt.Fprintf(&b, "  %s\n", acc.Render("SELECT folder_path, COUNT(*) FROM files GROUP BY folder_path;"))
	fmt.Fprintf(&b, "  %s\n", acc.Render("SELECT ext, COUNT(*), SUM(size) FROM files GROUP BY ext;"))
	fmt.Fprintf(&b, "  %s\n\n", acc.Render("SELECT name FROM files WHERE size > 100000000;"))

	fmt.Fprintf(&b, "%s\n", lbl.Render("Press any key to return"))

	return b.String()
}

func (m model) viewScan() string {
	elapsed := time.Since(m.start)

	// Calculate speed
	var speed string
	if elapsed > 0 {
		filesPerSec := float64(m.stats.files) / elapsed.Seconds()
		speed = formatSpeed(filesPerSec)
	} else {
		speed = "0"
	}

	elapsedStr := elapsed.Round(time.Second).String()

	var b strings.Builder

	// Create beautiful full-screen background
	width := m.getWidth()
	contentWidth := width - 6

	// Header with spinning animation and beautiful decorative border
	headerBox := lipgloss.NewStyle().
		Width(contentWidth).
		Align(lipgloss.Center).
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("#10b981")).
		Background(lipgloss.Color("#064e3b")).
		Padding(1, 2).
		MarginBottom(1)

	headerContent := fmt.Sprintf("%s %s", m.spin.View(),
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#6ee7b7")).Render("⚡ Scanning SharePoint Directory"))

	header := headerBox.Render(headerContent)
	fmt.Fprintf(&b, "%s\n", header)

	// Create beautiful stats cards
	statsBox := lipgloss.NewStyle().
		Width(contentWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#059669")).
		Background(lipgloss.Color("#022c22")).
		Padding(2, 3).
		MarginBottom(1)

	// Create individual stat cards
	filesCard := lipgloss.NewStyle().Foreground(lipgloss.Color("#6ee7b7")).Bold(true).Render(fmt.Sprintf("%d\nFiles", m.stats.files))
	foldersCard := lipgloss.NewStyle().Foreground(lipgloss.Color("#7dd3fc")).Bold(true).Render(fmt.Sprintf("%d\nFolders", m.stats.folders))
	speedCard := lipgloss.NewStyle().Foreground(lipgloss.Color("#fbbf24")).Bold(true).Render(fmt.Sprintf("%s/s\nSpeed", speed))
	elapsedCard := lipgloss.NewStyle().Foreground(lipgloss.Color("#f472b6")).Bold(true).Render(fmt.Sprintf("%s\nElapsed", elapsedStr))

	// Layout stats in a row
	statsRow := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(contentWidth/4).Align(lipgloss.Center).Render(filesCard),
		lipgloss.NewStyle().Width(contentWidth/4).Align(lipgloss.Center).Render(foldersCard),
		lipgloss.NewStyle().Width(contentWidth/4).Align(lipgloss.Center).Render(speedCard),
		lipgloss.NewStyle().Width(contentWidth/4).Align(lipgloss.Center).Render(elapsedCard))

	stats := statsBox.Render(statsRow)
	fmt.Fprintf(&b, "%s\n", stats)

	// Database path with beautiful colors
	fmt.Fprintf(&b, "%s %s\n",
		lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Bold(true).Render("💾 Database:"),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Render(m.dbPath))

	// Current file being processed with beautiful colors
	if m.stats.last != "" {
		fmt.Fprintf(&b, "%s %s\n",
			lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Bold(true).Render("🔍 Processing:"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("#f1f5f9")).Bold(true).Render(filepath.Base(m.stats.last)))
		fmt.Fprintf(&b, "%s %s\n",
			lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render("📁 Path:"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render(m.stats.last))
	}

	// Progress bar with percentage and estimated completion
	progressWidth := m.getProgressBarWidth()
	var progressBar string
	var progressText string

	if m.stats.estimatedTotal > 0 {
		// Show actual progress percentage
		progress := m.stats.progress
		filled := int(progress * float64(progressWidth) / 100)
		if filled > progressWidth {
			filled = progressWidth
		}
		filledBar := lipgloss.NewStyle().Foreground(lipgloss.Color("#10b981")).Render(strings.Repeat("█", filled))
		emptyBar := lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render(strings.Repeat("░", progressWidth-filled))
		progressBar = filledBar + emptyBar
		progressText = fmt.Sprintf("%.1f%% (%d/%d files)", progress, m.stats.files, m.stats.estimatedTotal)

		// Calculate time remaining
		if elapsed > 0 && progress > 0 {
			totalEstimatedTime := elapsed.Seconds() * 100 / progress
			remainingTime := time.Duration(totalEstimatedTime-elapsed.Seconds()) * time.Second
			if remainingTime > 0 {
				progressText += fmt.Sprintf(" • ~%s remaining", remainingTime.Round(time.Second))
			}
		}
	} else {
		// Show indeterminate progress
		filled := int(float64(progressWidth) * 0.6) // Show activity
		filledBar := lipgloss.NewStyle().Foreground(lipgloss.Color("#10b981")).Render(strings.Repeat("█", filled))
		emptyBar := lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render(strings.Repeat("░", progressWidth-filled))
		progressBar = filledBar + emptyBar
		progressText = "Scanning..."
	}

	fmt.Fprintf(&b, "\n%s %s\n",
		lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Bold(true).Render("⏳ Progress:"),
		progressBar)
	fmt.Fprintf(&b, "%s %s\n",
		lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render("         "), // Indent to align with "Progress:"
		lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Bold(true).Render(progressText))

	// Speed info with beautiful colors
	if elapsed > 0 {
		fmt.Fprintf(&b, "%s %.1f files/sec • %s elapsed\n",
			lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Bold(true).Render("⚡ Rate:"),
			float64(m.stats.files)/elapsed.Seconds(),
			elapsedStr)
	}

	// Help text with beautiful colors
	helpText := lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render("Press q/ESC to stop (safe)")
	fmt.Fprintf(&b, "\n%s\n", helpText)

	return b.String()
}

func (m model) viewDone() string {
	var b strings.Builder

	// Header
	if m.err == nil {
		fmt.Fprintf(&b, "%s %s\n\n",
			ok.Render("✓"),
			lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#22c55e")).Render("Cataloging Complete!"))
	} else {
		fmt.Fprintf(&b, "%s %s\n\n",
			bad.Render("✗"),
			lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ef4444")).Render("Completed with errors"))
		fmt.Fprintf(&b, "%s %v\n\n", lbl.Render("Error:"), m.err)
	}

	// Summary statistics
	elapsed := time.Since(m.start)
	var avgSpeed float64
	if elapsed > 0 {
		avgSpeed = float64(m.stats.files) / elapsed.Seconds()
	}

	// Results table
	fmt.Fprintf(&b, "┌─────────────────────────────────────────────────────────┐\n")
	fmt.Fprintf(&b, "│ %-55s │\n", val.Render("CATALOGING RESULTS"))
	fmt.Fprintf(&b, "├─────────────────────────────────────────────────────────┤\n")
	fmt.Fprintf(&b, "│ %-20s │ %-32s │\n", lbl.Render("Database Location:"), acc.Render(m.dbPath))
	fmt.Fprintf(&b, "├─────────────────────────────────────────────────────────┤\n")
	fmt.Fprintf(&b, "│ %-20s │ %-32s │\n", lbl.Render("Files Cataloged:"), val.Render(fmt.Sprintf("%d", m.stats.files)))
	fmt.Fprintf(&b, "│ %-20s │ %-32s │\n", lbl.Render("Folders Scanned:"), val.Render(fmt.Sprintf("%d", m.stats.folders)))
	fmt.Fprintf(&b, "│ %-20s │ %-32s │\n", lbl.Render("Time Elapsed:"), val.Render(elapsed.Round(time.Second).String()))
	fmt.Fprintf(&b, "│ %-20s │ %-32s │\n", lbl.Render("Average Speed:"), val.Render(fmt.Sprintf("%.1f files/sec", avgSpeed)))
	fmt.Fprintf(&b, "└─────────────────────────────────────────────────────────┘\n\n")

	// Performance visualization
	if m.stats.files > 0 {
		fmt.Fprintf(&b, "%s\n", val.Render("Performance Breakdown:"))

		// Simple bar chart showing relative performance
		maxWidth := m.getProgressBarWidth()

		// Files per second visualization
		speedBar := ""
		if avgSpeed > 0 {
			speedNormalized := int(avgSpeed * float64(maxWidth) / 100) // Normalize to 100 files/sec max
			if speedNormalized > maxWidth {
				speedNormalized = maxWidth
			}
			speedBar = strings.Repeat("█", speedNormalized) + strings.Repeat("░", maxWidth-speedNormalized)
		}
		fmt.Fprintf(&b, "%s %s [%s]\n", lbl.Render("Speed:"), acc.Render(speedBar), val.Render(fmt.Sprintf("%.1f/sec", avgSpeed)))

		// File volume visualization
		volumeNormalized := int(float64(m.stats.files) * float64(maxWidth) / 10000) // Normalize to 10k files max
		if volumeNormalized > maxWidth {
			volumeNormalized = maxWidth
		}
		volumeBar := strings.Repeat("█", volumeNormalized) + strings.Repeat("░", maxWidth-volumeNormalized)
		fmt.Fprintf(&b, "%s %s [%s]\n", lbl.Render("Volume:"), acc.Render(volumeBar), val.Render(fmt.Sprintf("%d files", m.stats.files)))
		fmt.Fprintf(&b, "\n")
	}

	// Next steps
	fmt.Fprintf(&b, "%s\n", val.Render("Next Steps:"))
	fmt.Fprintf(&b, "• %s\n", lbl.Render("Query your data: sqlite3 "+filepath.Base(m.dbPath)))
	fmt.Fprintf(&b, "• %s\n", lbl.Render("Find files: SELECT * FROM files WHERE name LIKE '%.pdf';"))
	fmt.Fprintf(&b, "• %s\n", lbl.Render("Analyze folders: SELECT COUNT(*) FROM files GROUP BY folder_path;"))
	fmt.Fprintf(&b, "• %s\n", lbl.Render("View schema: .schema"))

	fmt.Fprintf(&b, "\n%s\n", lbl.Render("Press any key to exit"))

	return b.String()
}

// ---------- scanning & DB ----------

func runScan(root, dbPath string, extFilter map[string]struct{}, hash bool) tea.Cmd {
	return func() tea.Msg {
		// First, estimate total files
		estimatedTotal := estimateFileCount(root, extFilter)

		err := scanAndPersist(root, dbPath, extFilter, hash, estimatedTotal, func(files, folders int64, last string, estimated int64) tea.Msg {
			return progressMsg{files: files, folders: folders, last: last, estimatedTotal: estimated}
		})
		return doneMsg{err: err}
	}
}

func estimateFileCount(root string, extFilter map[string]struct{}) int64 {
	var count int64

	// Quick estimation by walking the directory tree
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Continue on errors
		}

		if !d.IsDir() {
			ext := strings.ToLower(filepath.Ext(path))
			if len(extFilter) > 0 {
				if _, ok := extFilter[ext]; ok {
					count++
				}
			} else {
				count++
			}
		}
		return nil
	})

	return count
}

func scanAndPersist(root, dbPath string, extFilter map[string]struct{}, hash bool, estimatedTotal int64, progress func(int64, int64, string, int64) tea.Msg) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := initSchema(db); err != nil {
		return err
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA temp_store=MEMORY;`); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	folderStmt, err := tx.Prepare(`
		INSERT INTO folders(path, parent_path, mtime_utc)
		VALUES(?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET mtime_utc=excluded.mtime_utc
	`)
	if err != nil {
		return err
	}
	defer folderStmt.Close()

	fileStmt, err := tx.Prepare(`
		INSERT INTO files(abs_path, folder_path, name, ext, size, mtime_utc, mime, sha256)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(abs_path) DO UPDATE SET
		  size=excluded.size, mtime_utc=excluded.mtime_utc, mime=excluded.mime,
		  sha256=COALESCE(excluded.sha256, files.sha256)
	`)
	if err != nil {
		return err
	}
	defer fileStmt.Close()

	var files, dirs int64
	batch := 0
	root = filepath.Clean(root)

	errWalk := filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}

		if d.IsDir() {
			dirs++
			parent := filepath.Dir(p)
			if parent == p {
				parent = ""
			}
			mtime := info.ModTime().UTC().Format(time.RFC3339)
			if _, err := folderStmt.Exec(p, parent, mtime); err != nil {
				return err
			}
			batch++
			if batch >= 1000 {
				if err := tx.Commit(); err != nil {
					return err
				}
				progress(files, dirs, p, estimatedTotal)
				tx, err = db.Begin()
				if err != nil {
					return err
				}
				folderStmt, err = tx.Prepare(`
					INSERT INTO folders(path, parent_path, mtime_utc)
					VALUES(?, ?, ?)
					ON CONFLICT(path) DO UPDATE SET mtime_utc=excluded.mtime_utc
				`)
				if err != nil {
					return err
				}
				fileStmt, err = tx.Prepare(`
					INSERT INTO files(abs_path, folder_path, name, ext, size, mtime_utc, mime, sha256)
					VALUES(?, ?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT(abs_path) DO UPDATE SET
					  size=excluded.size, mtime_utc=excluded.mtime_utc, mime=excluded.mime,
					  sha256=COALESCE(excluded.sha256, files.sha256)
				`)
				if err != nil {
					return err
				}
				batch = 0
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(p))
		if len(extFilter) > 0 {
			if _, ok := extFilter[ext]; !ok {
				return nil
			}
		}

		files++
		dir := filepath.Dir(p)
		name := filepath.Base(p)
		size := info.Size()
		mtime := info.ModTime().UTC().Format(time.RFC3339)
		mimetype := detectMIME(ext)

		var sum *string
		if hash {
			s := hashFile(p)
			if s != "" {
				sum = &s
			}
		}

		if _, err := fileStmt.Exec(p, dir, name, ext, size, mtime, mimetype, sum); err != nil {
			return err
		}

		batch++
		if batch >= 1000 {
			if err := tx.Commit(); err != nil {
				return err
			}
			progress(files, dirs, p, estimatedTotal)
			tx, err = db.Begin()
			if err != nil {
				return err
			}
			folderStmt, err = tx.Prepare(`
				INSERT INTO folders(path, parent_path, mtime_utc)
				VALUES(?, ?, ?)
				ON CONFLICT(path) DO UPDATE SET mtime_utc=excluded.mtime_utc
			`)
			if err != nil {
				return err
			}
			fileStmt, err = tx.Prepare(`
				INSERT INTO files(abs_path, folder_path, name, ext, size, mtime_utc, mime, sha256)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(abs_path) DO UPDATE SET
				  size=excluded.size, mtime_utc=excluded.mtime_utc, mime=excluded.mime,
				  sha256=COALESCE(excluded.sha256, files.sha256)
			`)
			if err != nil {
				return err
			}
			batch = 0
		}

		return nil
	})
	if errWalk != nil {
		_ = tx.Rollback()
		return errWalk
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_files_ext ON files(ext);`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_files_folder ON files(folder_path);`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_files_mtime ON files(mtime_utc);`)

	progress(files, dirs, "", estimatedTotal)
	return nil
}

func initSchema(db *sql.DB) error {
	ddl := `
CREATE TABLE IF NOT EXISTS folders (
	path TEXT PRIMARY KEY,
	parent_path TEXT,
	mtime_utc TEXT
);
CREATE TABLE IF NOT EXISTS files (
	abs_path    TEXT PRIMARY KEY,
	folder_path TEXT NOT NULL,
	name        TEXT NOT NULL,
	ext         TEXT,
	size        INTEGER,
	mtime_utc   TEXT,
	mime        TEXT,
	sha256      TEXT
);
`
	_, err := db.Exec(ddl)
	return err
}

func parseExtSet(s string) map[string]struct{} {
	m := map[string]struct{}{}
	if s == "" {
		return m
	}
	for _, e := range strings.Split(s, ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		m[e] = struct{}{}
	}
	return m
}

func detectMIME(ext string) string {
	if ext == ".msg" {
		return "application/vnd.ms-outlook"
	}
	mt := mime.TypeByExtension(ext)
	if mt != "" {
		return mt
	}
	return "application/octet-stream"
}

func hashFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	_, _ = io.Copy(h, f)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// Speed formatter
func formatSpeed(filesPerSec float64) string {
	if filesPerSec < 1 {
		return fmt.Sprintf("%.2f", filesPerSec)
	} else if filesPerSec < 100 {
		return fmt.Sprintf("%.1f", filesPerSec)
	}
	return fmt.Sprintf("%.0f", filesPerSec)
}

// Responsive layout helpers
func (m model) getWidth() int {
	if m.windowSize.Width > 0 {
		return m.windowSize.Width
	}
	return 80 // Default width
}

func (m model) getHeight() int {
	if m.windowSize.Height > 0 {
		return m.windowSize.Height
	}
	return 24 // Default height
}

// Calculate responsive dimensions
func (m model) getProgressBarWidth() int {
	width := m.getWidth()
	if width < 60 {
		return 20
	} else if width < 100 {
		return 40
	}
	return 50
}

func (m model) getTableWidth() int {
	width := m.getWidth()
	if width < 70 {
		return 50
	} else if width < 100 {
		return 70
	}
	return 90
}

func (m model) getBrowserDisplayLines() int {
	height := m.getHeight()
	if height < 20 {
		return 8
	} else if height < 30 {
		return 15
	}
	return 20
}

// Responsive text wrapping
func (m model) wrapText(text string, maxWidth int) string {
	if len(text) <= maxWidth {
		return text
	}
	// Simple truncation with ellipsis
	if maxWidth > 3 {
		return text[:maxWidth-3] + "..."
	}
	return text[:maxWidth]
}

// Configuration management
func getConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".spcatalog_config.json")
}

func loadConfig() *appConfig {
	config := &appConfig{
		RecentPaths: []string{},
		MaxRecent:   9, // Support 1-9 number shortcuts
	}

	configPath := getConfigPath()
	if configPath == "" {
		return config
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return config // Return default if file doesn't exist or can't be read
	}

	if err := json.Unmarshal(data, config); err != nil {
		return &appConfig{RecentPaths: []string{}, MaxRecent: 9} // Return default on parse error
	}

	return config
}

func saveConfig(config *appConfig) error {
	configPath := getConfigPath()
	if configPath == "" {
		return fmt.Errorf("unable to determine config path")
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}

// Add path to recent paths, maintaining uniqueness and max count
func addToRecentPaths(paths []string, newPath string, maxRecent int) []string {
	if newPath == "" {
		return paths
	}

	// Remove if already exists (to move to front)
	filtered := make([]string, 0, len(paths))
	for _, p := range paths {
		if p != newPath {
			filtered = append(filtered, p)
		}
	}

	// Add to front
	result := append([]string{newPath}, filtered...)

	// Limit to maxRecent
	if len(result) > maxRecent {
		result = result[:maxRecent]
	}

	return result
}
