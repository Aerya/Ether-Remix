package core

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Lyrics are a playable item's words: timed lines when the source has them,
// otherwise plain text. Any media class may have them (songs, podcasts).
type Lyrics struct {
	Lines        []LyricLine
	Plain        string
	Instrumental bool
	// Source names where they came from, for attribution ("LRCLIB").
	Source string
}

// LyricLine is one line: when it starts and ends (End is zero when unknown),
// its words with their own timing when the source has them, and optional
// translation and romanization lines.
type LyricLine struct {
	Start        time.Duration
	End          time.Duration
	Text         string
	Words        []LyricWord
	Translation  string
	Romanization string
}

// LyricWord is one word of a line and when it is sung (End zero when unknown).
type LyricWord struct {
	Start time.Duration
	End   time.Duration
	Text  string
}

// lrcWord is an enhanced-LRC word stamp ("<mm:ss.xx>").
var lrcWord = regexp.MustCompile(`<(\d{1,3}):(\d{1,2})(?:[.:](\d{1,3}))?>`)

var lrcLine = regexp.MustCompile(`^((?:\[\d{1,3}:\d{1,2}(?:[.:]\d{1,3})?\])+)(.*)$`)
var lrcStamp = regexp.MustCompile(`\[(\d{1,3}):(\d{1,2})(?:[.:](\d{1,3}))?\]`)

// ParseLRC reads LRC timed lyrics ("[mm:ss.xx] words"), one line per stamp,
// in time order. Metadata tags and untimed lines are dropped. Enhanced LRC
// word stamps ("<mm:ss.xx>word") become the line's words. Each line ends
// where the next begins; each word where the next word begins.
func ParseLRC(lrc string) []LyricLine {
	var out []LyricLine
	for _, raw := range strings.Split(lrc, "\n") {
		m := lrcLine.FindStringSubmatch(strings.TrimSpace(raw))
		if m == nil {
			continue
		}
		text := strings.TrimSpace(m[2])
		for _, stamp := range lrcStamp.FindAllStringSubmatch(m[1], -1) {
			minutes, _ := strconv.Atoi(stamp[1])
			seconds, _ := strconv.Atoi(stamp[2])
			fraction := time.Duration(0)
			if stamp[3] != "" {
				digits, _ := strconv.Atoi(stamp[3])
				fraction = time.Duration(digits) * time.Second / time.Duration(pow10(len(stamp[3])))
			}
			line := LyricLine{Start: time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second + fraction, Text: text}
			if lrcWord.MatchString(text) {
				line.Words = parseLRCWords(text)
				parts := make([]string, 0, len(line.Words))
				for _, w := range line.Words {
					parts = append(parts, w.Text)
				}
				line.Text = strings.Join(parts, " ")
			}
			out = append(out, line)
		}
	}
	for i := 1; i < len(out); i++ { // stable insertion sort: lines repeat stamps out of order
		for j := i; j > 0 && out[j].Start < out[j-1].Start; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	for i := range out {
		if i+1 < len(out) {
			out[i].End = out[i+1].Start
		}
		if n := len(out[i].Words); n > 0 && out[i].Words[n-1].End == 0 {
			out[i].Words[n-1].End = out[i].End
		}
	}
	return out
}

// parseLRCWords splits an enhanced-LRC line into its stamped words.
func parseLRCWords(text string) []LyricWord {
	locs := lrcWord.FindAllStringSubmatchIndex(text, -1)
	var words []LyricWord
	for n, loc := range locs {
		end := len(text)
		if n+1 < len(locs) {
			end = locs[n+1][0]
		}
		word := strings.TrimSpace(text[loc[1]:end])
		if word == "" {
			continue
		}
		minutes, _ := strconv.Atoi(text[loc[2]:loc[3]])
		seconds, _ := strconv.Atoi(text[loc[4]:loc[5]])
		fraction := time.Duration(0)
		if loc[6] >= 0 {
			digits, _ := strconv.Atoi(text[loc[6]:loc[7]])
			fraction = time.Duration(digits) * time.Second / time.Duration(pow10(loc[7]-loc[6]))
		}
		start := time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second + fraction
		if k := len(words); k > 0 && words[k-1].End == 0 {
			words[k-1].End = start
		}
		words = append(words, LyricWord{Start: start, Text: word})
	}
	return words
}

func pow10(n int) int {
	p := 1
	for range n {
		p *= 10
	}
	return p
}
