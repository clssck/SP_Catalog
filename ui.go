package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Color palette - Modern, professional, with great contrast
var (
	// Primary colors
	primary   = lipgloss.Color("#7c3aed") // Purple
	secondary = lipgloss.Color("#06b6d4") // Cyan
	accent    = lipgloss.Color("#10b981") // Emerald

	// Semantic colors
	success = lipgloss.Color("#22c55e") // Green
	warning = lipgloss.Color("#f59e0b") // Amber
	danger  = lipgloss.Color("#ef4444") // Red
	info    = lipgloss.Color("#3b82f6") // Blue

	// Neutral colors
	background = lipgloss.Color("#0f172a") // Slate-900
	surface    = lipgloss.Color("#1e293b") // Slate-800
	border     = lipgloss.Color("#334155") // Slate-700
	muted      = lipgloss.Color("#64748b") // Slate-500
	text       = lipgloss.Color("#f1f5f9") // Slate-100
	textMuted  = lipgloss.Color("#94a3b8") // Slate-400
)

// Typography styles
var (
	titleStyle = lipgloss.NewStyle().
			Foreground(text).
			Bold(true).
			MarginBottom(1)

	headingStyle = lipgloss.NewStyle().
			Foreground(primary).
			Bold(true)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(textMuted)

	labelStyle = lipgloss.NewStyle().
			Foreground(textMuted).
			Bold(true)

	valueStyle = lipgloss.NewStyle().
			Foreground(text).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(danger).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(success).
			Bold(true)

	accentStyle = lipgloss.NewStyle().
			Foreground(accent).
			Bold(true)
)

// Layout components
var (
	containerStyle = lipgloss.NewStyle().
			Background(background).
			Padding(1, 2).
			Width(100).
			Height(30)

	panelStyle = lipgloss.NewStyle().
			Background(surface).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Padding(1, 2).
			MarginRight(1).
			MarginBottom(1)

	cardStyle = lipgloss.NewStyle().
			Background(surface).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Padding(2, 3).
			MarginBottom(1)

	inputStyle = lipgloss.NewStyle().
			Background(surface).
			Foreground(text).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Padding(0, 1).
			MarginRight(1).
			Width(50)

	inputFocusStyle = lipgloss.NewStyle().
			Background(surface).
			Foreground(text).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(primary).
			Padding(0, 1).
			MarginRight(1).
			Width(50)

	buttonStyle = lipgloss.NewStyle().
			Background(primary).
			Foreground(text).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(primary).
			Padding(0, 2).
			Bold(true).
			MarginRight(1)

	buttonHoverStyle = lipgloss.NewStyle().
				Background(accent).
				Foreground(background).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(accent).
				Padding(0, 2).
				Bold(true).
				MarginRight(1)

	progressBarStyle = lipgloss.NewStyle().
				Background(surface).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(border).
				Padding(0, 1).
				Width(50)

	statsCardStyle = lipgloss.NewStyle().
			Background(surface).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Padding(1, 2).
			MarginRight(1).
			Width(20).
			Height(6)
)

// UI helper functions
func renderTitle(title string) string {
	gradient := lipgloss.NewStyle().
		Foreground(primary).
		Bold(true)

	return gradient.Render(title)
}

func renderHeader() string {
	logo := `
    ███████╗██████╗      ██████╗ █████╗ ████████╗ █████╗ ██╗      ██████╗  ██████╗ 
    ██╔════╝██╔══██╗    ██╔════╝██╔══██╗╚══██╔══╝██╔══██╗██║     ██╔═══██╗██╔════╝ 
    ███████╗██████╔╝    ██║     ███████║   ██║   ███████║██║     ██║   ██║██║  ███╗
    ╚════██║██╔═══╝     ██║     ██╔══██║   ██║   ██╔══██║██║     ██║   ██║██║   ██║
    ███████║██║         ╚██████╗██║  ██║   ██║   ██║  ██║███████╗╚██████╔╝╚██████╔╝
    ╚══════╝╚═╝          ╚═════╝╚═╝  ╚═╝   ╚═╝   ╚═╝  ╚═╝╚══════╝ ╚═════╝  ╚═════╝`

	logoStyle := lipgloss.NewStyle().
		Foreground(primary).
		Bold(true)

	subtitle := subtitleStyle.Render("SharePoint → SQLite • Fast • Beautiful • Functional")

	return lipgloss.JoinVertical(lipgloss.Center,
		logoStyle.Render(logo),
		"",
		subtitle,
	)
}

func renderProgressCard(title, value, subtitle string, color lipgloss.Color) string {
	titleStyle := headingStyle.Copy().Foreground(color)
	valueStyle := lipgloss.NewStyle().
		Foreground(color).
		Bold(true).
		Render(value)

	content := lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render(title),
		valueStyle,
		subtitleStyle.Render(subtitle),
	)

	return statsCardStyle.Copy().
		BorderForeground(color).
		Render(content)
}

func renderStatsGrid(files, folders int64, speed, elapsed string) string {
	filesCard := renderProgressCard("Files", fmt.Sprintf("%d", files), "cataloged", accent)
	foldersCard := renderProgressCard("Folders", fmt.Sprintf("%d", folders), "scanned", secondary)
	speedCard := renderProgressCard("Speed", speed, "files/sec", info)
	timeCard := renderProgressCard("Elapsed", elapsed, "duration", warning)

	topRow := lipgloss.JoinHorizontal(lipgloss.Top, filesCard, foldersCard)
	bottomRow := lipgloss.JoinHorizontal(lipgloss.Top, speedCard, timeCard)

	return lipgloss.JoinVertical(lipgloss.Left, topRow, bottomRow)
}

func renderKeyHelp(keys []string) string {
	var parts []string
	colors := []lipgloss.Color{primary, accent, secondary, info}

	for i, key := range keys {
		keyStyle := lipgloss.NewStyle().
			Background(colors[i%len(colors)]).
			Foreground(background).
			Padding(0, 1).
			Bold(true).
			MarginRight(1)

		parts = append(parts, keyStyle.Render(key))
	}

	return lipgloss.JoinHorizontal(lipgloss.Left, parts...)
}

func renderBorder(content string, title string, color lipgloss.Color) string {
	titleBar := lipgloss.NewStyle().
		Background(color).
		Foreground(background).
		Bold(true).
		Padding(0, 1).
		MarginBottom(1).
		Render(fmt.Sprintf(" %s ", title))

	bordered := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Padding(1, 2)

	return lipgloss.JoinVertical(lipgloss.Left,
		titleBar,
		bordered.Render(content),
	)
}

func renderTable(headers []string, rows [][]string) string {
	if len(rows) == 0 {
		return subtitleStyle.Render("No data to display")
	}

	// Calculate column widths
	colWidths := make([]int, len(headers))
	for i, header := range headers {
		colWidths[i] = lipgloss.Width(header)
	}

	for _, row := range rows {
		for i, cell := range row {
			if i < len(colWidths) {
				width := lipgloss.Width(cell)
				if width > colWidths[i] {
					colWidths[i] = width
				}
			}
		}
	}

	// Render header
	var headerCells []string
	for i, header := range headers {
		style := headingStyle.Copy().Width(colWidths[i]).Align(lipgloss.Left)
		headerCells = append(headerCells, style.Render(header))
	}
	headerRow := lipgloss.JoinHorizontal(lipgloss.Left, headerCells...)

	// Render separator
	var sepCells []string
	for _, width := range colWidths {
		sepCells = append(sepCells, strings.Repeat("─", width))
	}
	separator := lipgloss.NewStyle().Foreground(border).Render(strings.Join(sepCells, "─┼─"))

	// Render rows
	var renderedRows []string
	for _, row := range rows {
		var cells []string
		for i, cell := range row {
			if i < len(colWidths) {
				style := lipgloss.NewStyle().
					Foreground(text).
					Width(colWidths[i]).
					Align(lipgloss.Left)
				cells = append(cells, style.Render(cell))
			}
		}
		renderedRows = append(renderedRows, lipgloss.JoinHorizontal(lipgloss.Left, cells...))
	}

	// Combine all parts
	var parts []string
	parts = append(parts, headerRow)
	parts = append(parts, separator)
	parts = append(parts, renderedRows...)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// Progress bar component
func renderProgressBar(percent float64, width int) string {
	if width <= 0 {
		width = 40
	}

	filled := int(percent * float64(width) / 100)
	if filled > width {
		filled = width
	}

	progressStyle := lipgloss.NewStyle().
		Foreground(accent).
		Background(surface)

	emptyStyle := lipgloss.NewStyle().
		Foreground(muted).
		Background(surface)

	filledBar := progressStyle.Render(strings.Repeat("█", filled))
	emptyBar := emptyStyle.Render(strings.Repeat("░", width-filled))

	return lipgloss.JoinHorizontal(lipgloss.Left, filledBar, emptyBar)
}

// File size formatter
func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
