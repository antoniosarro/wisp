package tui

import (
	"os"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"
)

// The palette comes from the logo: a magenta→cyan gradient over a violet
// panel, with softer tones for text, borders, and status.
var (
	lightTheme    = os.Getenv("WISP_THEME") == "light"
	colorMagenta  = themeColor("#D741F5", "#9B1FB5")
	colorViolet   = themeColor("#6252F0", "#4B3BD0")
	colorLavender = themeColor("#A29BF5", "#5B4FC4")
	colorCyan     = themeColor("#50DCEB", "#0B7E8C")
	colorMint     = themeColor("#5EE6B0", "#12825A")
	colorRose     = themeColor("#FF5C8A", "#C0204F")
	colorAmber    = themeColor("#FFB86C", "#A85A00")
	colorText     = themeColor("#F9F9FF", "#1C1A33")
	colorDim      = themeColor("#8E8BB0", "#6B6890")
	colorMuted    = themeColor("#4A4775", "#B8B4DC")
	colorPanel    = themeColor("#26243F", "#ECEBFA")

	styleUserPrompt      = lipgloss.NewStyle().Foreground(colorText).Background(colorPanel)
	styleUserBar         = lipgloss.NewStyle().Foreground(colorMagenta).Background(colorPanel)
	styleAnswerPrefix    = lipgloss.NewStyle().Foreground(colorCyan)
	styleNoticePrefix    = lipgloss.NewStyle().Foreground(colorDim)
	styleReasoning       = lipgloss.NewStyle().Foreground(colorDim).Italic(true)
	styleReasoningHeader = lipgloss.NewStyle().Foreground(colorLavender).Bold(true)
	styleReasoningBorder = lipgloss.NewStyle().Foreground(colorMuted)
	styleSpinner         = lipgloss.NewStyle().Foreground(colorMagenta)

	styleToolCard        = boxStyle(colorMuted)
	styleToolCardRunning = boxStyle(colorLavender)
	styleToolCardFailed  = boxStyle(colorRose)
	styleToolCardDenied  = boxStyle(colorAmber)
	styleInputBox        = boxStyle(colorMuted)
	styleChatBox         = boxStyle(colorMuted)

	styleToolText   = lipgloss.NewStyle().Bold(true)
	styleToolDetail = lipgloss.NewStyle().Foreground(colorDim)
	styleToolOK     = lipgloss.NewStyle().Foreground(colorMint)
	styleToolFailed = lipgloss.NewStyle().Foreground(colorRose)
	styleToolDenied = lipgloss.NewStyle().Foreground(colorAmber)
	styleToolError  = lipgloss.NewStyle().Foreground(colorRose)
	styleInlineCode = lipgloss.NewStyle().Foreground(colorCyan).Background(colorPanel).Padding(0, 1)
	styleBullet     = lipgloss.NewStyle().Foreground(colorText)
	styleDirName    = lipgloss.NewStyle().Bold(true).Foreground(colorLavender)

	styleError      = lipgloss.NewStyle().Bold(true).Foreground(colorRose)
	styleBorderLine = lipgloss.NewStyle().Foreground(colorMuted)
	styleDim        = lipgloss.NewStyle().Foreground(colorDim)
	styleChatMargin = lipgloss.NewStyle().PaddingLeft(chatMarginLeft)
)

// The chat's left margin and the marker column before message text.
const (
	chatMarginLeft  = 2
	chatPrefixWidth = 2 // message marker and following space
	chatContentLeft = chatMarginLeft + chatPrefixWidth
)

// themeColor picks the dark or light variant for $WISP_THEME.
func themeColor(dark, light string) lipgloss.Color {
	if lightTheme {
		return lipgloss.Color(light)
	}
	return lipgloss.Color(dark)
}

// inputStyles drops textarea's default cursor-line background, which made
// the prompt look permanently highlighted.
func inputStyles() (focused, blurred textarea.Style) {
	focused = textarea.Style{
		Placeholder: lipgloss.NewStyle().Foreground(colorDim),
		Prompt:      lipgloss.NewStyle().Foreground(colorMagenta),
	}
	focused.Text = lipgloss.NewStyle().Foreground(colorText)
	blurred = focused
	blurred.Text = lipgloss.NewStyle().Foreground(colorDim)
	return focused, blurred
}

// boxStyle is the rounded, padded border shared by cards and panels.
func boxStyle(border lipgloss.Color) lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1)
}

// boxed renders body in style at the given outer width (border excluded).
func boxed(style lipgloss.Style, width int, body string) string {
	return style.Width(max(10, width-2)).Render(body)
}
