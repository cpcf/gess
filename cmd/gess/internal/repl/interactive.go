package repl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

var errLineInterrupted = errors.New("line interrupted")
var errInteractiveExit = errors.New("interactive exit requested")

type promptMode int

const (
	promptModeInput promptMode = iota
	promptModeSearch
)

const maxPromptSuggestions = 80
const ctrlCExitWindow = time.Second
const interactiveHelpLine = "Up/Down history | Ctrl-R search"

type ctrlCExitExpiredMsg struct{}

func runInteractive(ctx context.Context, state *replState, in io.Reader, out io.Writer, opts Options) error {
	historyPath := defaultReplHistoryPath()
	history := loadReplHistory(historyPath, defaultHistoryLimit)
	defer saveReplHistory(historyPath, history)

	for {
		line, err := readInteractiveLine(ctx, state, history, in, out, opts.Prompt)
		switch {
		case errors.Is(err, io.EOF):
			return nil
		case errors.Is(err, errInteractiveExit):
			fmt.Fprintln(out)
			return nil
		case errors.Is(err, errLineInterrupted):
			fmt.Fprintln(out)
			continue
		case err != nil:
			return err
		}

		echoInteractiveLine(out, opts.Prompt, line)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		history.add(trimmed)
		exit, err := state.exec(ctx, trimmed)
		if err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
		}
		if exit {
			return nil
		}
	}
}

func echoInteractiveLine(out io.Writer, prompt, line string) {
	fmt.Fprintf(out, "%s%s\n", prompt, line)
}

func readInteractiveLine(ctx context.Context, state *replState, history *replHistory, in io.Reader, out io.Writer, prompt string) (string, error) {
	model := newPromptModel(ctx, state, history, prompt)
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out))
	final, err := program.Run()
	if err != nil {
		if errors.Is(err, tea.ErrInterrupted) {
			return "", errLineInterrupted
		}
		return "", err
	}
	model, ok := final.(*promptModel)
	if !ok {
		return "", fmt.Errorf("interactive prompt returned %T", final)
	}
	switch {
	case model.eof:
		return "", io.EOF
	case model.exit:
		return "", errInteractiveExit
	case model.interrupted:
		return "", errLineInterrupted
	default:
		return model.line, nil
	}
}

type promptModel struct {
	ctx       context.Context
	state     *replState
	history   *replHistory
	navigator historyNavigator

	input  textinput.Model
	search textinput.Model

	mode          promptMode
	searchMatches []string
	searchIndex   int

	line        string
	eof         bool
	exit        bool
	interrupted bool
	ctrlCExit   bool
	width       int
}

func newPromptModel(ctx context.Context, state *replState, history *replHistory, prompt string) *promptModel {
	input := textinput.New()
	input.Prompt = prompt
	input.ShowSuggestions = true
	input.KeyMap.NextSuggestion.SetKeys("ctrl+n")
	input.KeyMap.PrevSuggestion.SetKeys("ctrl+p")

	search := textinput.New()
	search.Prompt = "reverse-i-search> "
	search.ShowSuggestions = false

	model := &promptModel{
		ctx:       ctx,
		state:     state,
		history:   history,
		navigator: newHistoryNavigator(history),
		input:     input,
		search:    search,
	}
	model.refreshSuggestions()
	return model
}

func (m *promptModel) Init() tea.Cmd {
	return m.input.Focus()
}

func (m *promptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.applyWidth()
	case ctrlCExitExpiredMsg:
		m.ctrlCExit = false
		return m, nil
	case tea.KeyPressMsg:
		if m.mode == promptModeSearch {
			return m.updateSearch(msg)
		}
		switch msg.String() {
		case "enter":
			m.line = m.input.Value()
			return m, tea.Quit
		case "ctrl+c":
			if strings.TrimSpace(m.input.Value()) == "" {
				if m.ctrlCExit {
					m.exit = true
					return m, tea.Quit
				}
				m.ctrlCExit = true
				return m, tea.Tick(ctrlCExitWindow, func(time.Time) tea.Msg {
					return ctrlCExitExpiredMsg{}
				})
			}
			m.interrupted = true
			return m, tea.Quit
		case "ctrl+d":
			if m.input.Value() == "" {
				m.eof = true
				return m, tea.Quit
			}
		case "ctrl+l":
			return m, tea.ClearScreen
		case "ctrl+r":
			return m, m.startSearch()
		case "up":
			if value, ok := m.navigator.previous(m.input.Value()); ok {
				m.setInputValue(value)
			}
			return m, nil
		case "down":
			if value, ok := m.navigator.next(); ok {
				m.setInputValue(value)
			}
			return m, nil
		}
	}

	old := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != old {
		m.ctrlCExit = false
		m.navigator.reset(m.input.Value())
	}
	m.refreshSuggestions()
	return m, cmd
}

func (m *promptModel) View() tea.View {
	if m.mode == promptModeSearch {
		line := m.input.View() + "\n" + m.search.View()
		if match := m.currentSearchMatch(); match != "" {
			line += "  " + match
		} else {
			line += "  no matches"
		}
		return tea.NewView(line)
	}
	if m.ctrlCExit {
		return tea.NewView(m.input.View() + "\n" + m.promptStyle("Press Ctrl-C again to exit"))
	}
	return tea.NewView(m.input.View() + "\n" + m.promptStyle(interactiveHelpLine))
}

func (m *promptModel) promptStyle(text string) string {
	styles := m.input.Styles()
	if m.input.Focused() {
		return styles.Focused.Prompt.Render(text)
	}
	return styles.Blurred.Prompt.Render(text)
}

func (m *promptModel) updateSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if match := m.currentSearchMatch(); match != "" {
			m.setInputValue(match)
		}
		return m, m.stopSearch()
	case "esc":
		return m, m.stopSearch()
	case "ctrl+c":
		m.interrupted = true
		return m, tea.Quit
	case "ctrl+r", "up":
		m.nextSearchMatch()
		return m, nil
	case "down":
		m.previousSearchMatch()
		return m, nil
	}

	old := m.search.Value()
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	if m.search.Value() != old {
		m.refreshSearchMatches()
	}
	return m, cmd
}

func (m *promptModel) startSearch() tea.Cmd {
	m.mode = promptModeSearch
	m.input.Blur()
	m.search.Reset()
	m.refreshSearchMatches()
	return m.search.Focus()
}

func (m *promptModel) stopSearch() tea.Cmd {
	m.mode = promptModeInput
	m.search.Blur()
	m.refreshSuggestions()
	return m.input.Focus()
}

func (m *promptModel) refreshSearchMatches() {
	needle := strings.ToLower(m.search.Value())
	entries := m.history.snapshot()
	m.searchMatches = m.searchMatches[:0]
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if needle == "" || strings.Contains(strings.ToLower(entry), needle) {
			m.searchMatches = append(m.searchMatches, entry)
		}
	}
	m.searchIndex = 0
}

func (m *promptModel) currentSearchMatch() string {
	if len(m.searchMatches) == 0 {
		return ""
	}
	if m.searchIndex < 0 {
		m.searchIndex = 0
	}
	if m.searchIndex >= len(m.searchMatches) {
		m.searchIndex = len(m.searchMatches) - 1
	}
	return m.searchMatches[m.searchIndex]
}

func (m *promptModel) nextSearchMatch() {
	if len(m.searchMatches) == 0 {
		return
	}
	m.searchIndex++
	if m.searchIndex >= len(m.searchMatches) {
		m.searchIndex = 0
	}
}

func (m *promptModel) previousSearchMatch() {
	if len(m.searchMatches) == 0 {
		return
	}
	m.searchIndex--
	if m.searchIndex < 0 {
		m.searchIndex = len(m.searchMatches) - 1
	}
}

func (m *promptModel) setInputValue(value string) {
	m.input.SetValue(value)
	m.input.CursorEnd()
	m.refreshSuggestions()
}

func (m *promptModel) refreshSuggestions() {
	value := m.input.Value()
	if m.input.Position() != utf8.RuneCountInString(value) {
		m.input.SetSuggestions(nil)
		return
	}
	suggestions := completeLine(m.ctx, m.state, value)
	if len(suggestions) > maxPromptSuggestions {
		suggestions = suggestions[:maxPromptSuggestions]
	}
	m.input.SetSuggestions(suggestions)
}

func (m *promptModel) applyWidth() {
	if m.width <= 0 {
		return
	}
	inputWidth := max(m.width-utf8.RuneCountInString(m.input.Prompt)-1, 20)
	m.input.SetWidth(inputWidth)

	searchWidth := max(m.width-utf8.RuneCountInString(m.search.Prompt)-1, 20)
	m.search.SetWidth(searchWidth)
}
