package wakabox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/YouEclipse/wakatime-go/pkg/wakatime"
	"github.com/google/go-github/github"
)

// maxLineLength is the visible number of characters in a pinned gist box
const maxLineLength = 53

// BarStyle defines valid styles for the progress bar
var BarStyle = map[string][]rune{
	"SOLIDLT":    []rune(`░▏▎▍▌▋▊▉█`),
	"SOLIDMD":    []rune(`▒▏▎▍▌▋▊▉█`),
	"SOLIDDK":    []rune(`▓▏▎▍▌▋▊▉█`),
	"EMPTY":      []rune(` ▏▎▍▌▋▊▉█`),
	"UNDERSCORE": []rune(`▁▏▎▍▌▋▊▉█`),
}

type BoxStyle struct {
	BarStyle     string
	BarLength    string
	TimeStyle    string
	barLengthInt int
	maxLangLen   int
	maxTimeLen   int
}

type Box struct {
	github     *github.Client
	wakaAPIKey string
	style      BoxStyle
}

func NewBox(wakaAPIKey, ghUsername, ghToken string, style BoxStyle) *Box {
	box := &Box{
		wakaAPIKey: wakaAPIKey,
	}

	tp := github.BasicAuthTransport{
		Username: strings.TrimSpace(ghUsername),
		Password: strings.TrimSpace(ghToken),
	}
	box.github = github.NewClient(tp.Client())

	length, err := strconv.Atoi(style.BarLength)
	if err != nil {
		length = 21
	}
	style.barLengthInt = length
	if style.BarStyle == "" {
		style.BarStyle = "SOLIDLT"
	}
	box.style = style

	return box
}

type wakaStatsResponse struct {
	Data struct {
		Languages []wakatime.StatItem `json:"languages"`
	} `json:"data"`
}

// GetStats calls the WakaTime API directly. Since 2026-11-01 WakaTime API keys
// are write-only (plugins), so reading stats requires an OAuth access token
// (https://wakatime.com/oauth) with the read_stats scope, sent as a Bearer token.
func (b *Box) GetStats(ctx context.Context) ([]string, error) {
	url := "https://wakatime.com/api/v1/users/current/stats/last_7_days"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("wakabox.GetStats: error creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(b.wakaAPIKey))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wakabox.GetStats: error executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("wakabox.GetStats: API error (status %d): %s", resp.StatusCode, string(body))
	}

	var statsRes wakaStatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&statsRes); err != nil {
		return nil, fmt.Errorf("wakabox.GetStats: error decoding json: %w", err)
	}

	if languages := statsRes.Data.Languages; len(languages) > 0 {
		lines, err := b.GenerateGistLines(ctx, languages)
		if err != nil {
			return nil, fmt.Errorf("wakabox.GetStats: Error generating gist lines: %w", err)
		}
		return lines, nil
	}
	return []string{"Still Gathering Statistics..."}, nil
}

func (b *Box) GetGist(ctx context.Context, id string) (*github.Gist, error) {
	gist, _, err := b.github.Gists.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("wakabox.GetGist: Error getting gist from github: %w", err)
	}
	return gist, nil
}

func (b *Box) UpdateGist(ctx context.Context, id string, gist *github.Gist) error {
	_, _, err := b.github.Gists.Edit(ctx, id, gist)
	if err != nil {
		return fmt.Errorf("wakabox.UpdateGist: Error updating gist: %w", err)
	}
	return nil
}

func (b *Box) UpdateMarkdown(ctx context.Context, title, filename string, content []byte) error {
	md, err := ioutil.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("wakabox.UpdateMarkdown: Error reade a file: %w", err)
	}

	start := []byte("<!-- waka-box start -->")
	before := md[:bytes.Index(md, start)+len(start)]
	end := []byte("<!-- waka-box end -->")
	after := md[bytes.Index(md, end):]

	newMd := bytes.NewBuffer(nil)
	newMd.Write(before)
	newMd.WriteString("\n" + title + "\n")
	newMd.WriteString("```text\n")
	newMd.Write(content)
	newMd.WriteString("\n")
	newMd.WriteString("```\n")
	newMd.WriteString("<!-- Powered by https://github.com/YouEclipse/waka-box-go . -->\n")
	newMd.Write(after)

	err = ioutil.WriteFile(filename, newMd.Bytes(), os.ModeAppend)
	if err != nil {
		return fmt.Errorf("wakabox.UpdateMarkdown: Error write a file: %w", err)
	}

	return nil
}

func (b *Box) GenerateGistLines(ctx context.Context, languages []wakatime.StatItem) ([]string, error) {
	max := 0
	lines := make([]string, 0)
	for _, stat := range languages {
		if b.style.TimeStyle == "SHORT" {
			*stat.Text = convertDuration(*stat.Text)
		}
		if b.style.maxTimeLen < len(*stat.Text) {
			b.style.maxTimeLen = len(*stat.Text)
		}
		if b.style.maxLangLen < len(*stat.Name) {
			b.style.maxLangLen = len(*stat.Name)
		}
	}
	if b.style.barLengthInt < 0 {
		b.style.barLengthInt = maxLineLength - (b.style.maxLangLen + b.style.maxTimeLen + 10)
	}
	for _, stat := range languages {
		if max >= 5 {
			break
		}
		lines = append(lines, b.ConstructLine(ctx, stat))
		max++
	}
	return lines, nil
}

func (b *Box) ConstructLine(ctx context.Context, stat wakatime.StatItem) string {
	return fmt.Sprintf("%-*s🕓 %-*s%s%5.1f%%",
		b.style.maxLangLen+1, *stat.Name,
		b.style.maxTimeLen+1, *stat.Text,
		GenerateBarChart(ctx, *stat.Percent, b.style.barLengthInt, b.style.BarStyle),
		*stat.Percent)
}

func GenerateBarChart(ctx context.Context, percent float64, size int, barType string) string {
	syms := BarStyle[barType]
	if len(syms) > 9 {
		panic("No Syms")
	}

	frac := int(math.Floor((float64(size) * 8 * percent) / 100))
	barsFull := int(math.Floor(float64(frac) / 8))

	if barsFull >= size {
		return strings.Repeat(string(syms[8:9]), size)
	}

	var semi = frac % 8
	bar := strings.Repeat(string(syms[8:9]), barsFull) + string(syms[semi:semi+1])

	return pad(bar, string(syms[0:1]), size)
}

func pad(s, pad string, targetLength int) string {
	padding := targetLength - utf8.RuneCountInString(s)
	if padding <= 0 {
		return s
	}
	return s + strings.Repeat(pad, padding)
}

func convertDuration(t string) string {
	r := strings.NewReplacer(
		"hr", "h",
		"min", "m",
		"sec", "s",
		" ", "",
		"s", "",
	)
	return r.Replace(t)
}
