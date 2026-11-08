// Package wordpiece is the BERT WordPiece tokenizer, in pure Go, for the embedding
// service. MiniLM-L6-v2 uses the uncased BERT vocabulary, so the steps follow BERT's
// BasicTokenizer and WordpieceTokenizer exactly:
//
//  1. clean: drop control characters, turn any whitespace into a space;
//  2. lowercase and strip accents (decompose, remove combining marks);
//  3. put spaces around CJK characters and around punctuation, then split on spaces;
//  4. WordPiece each word: greedy longest-match against the vocabulary, continuation
//     pieces carry a "##" prefix, and a word that cannot be matched becomes [UNK].
//
// The output is [CLS] pieces... [SEP], truncated to a maximum length. A tokenizer that
// differs from the one the model was trained with does not fail loudly: it silently
// degrades the vectors. That is why the test file pins known ids from the real
// vocabulary.
package wordpiece

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Special tokens.
const (
	unk = "[UNK]"
	cls = "[CLS]"
	sep = "[SEP]"
	pad = "[PAD]"
)

// maxWordLength is BERT's limit: a longer "word" is unknown.
const maxWordLength = 100

// Tokenizer turns text into vocabulary ids.
type Tokenizer struct {
	vocab map[string]int64
	// ids of the special tokens
	UnkID, ClsID, SepID, PadID int64
}

// New builds a Tokenizer from a vocabulary (token to id).
func New(vocab map[string]int64) (*Tokenizer, error) {
	t := &Tokenizer{vocab: vocab}
	var ok [4]bool
	t.UnkID, ok[0] = vocab[unk]
	t.ClsID, ok[1] = vocab[cls]
	t.SepID, ok[2] = vocab[sep]
	t.PadID, ok[3] = vocab[pad]
	if !ok[0] || !ok[1] || !ok[2] || !ok[3] {
		return nil, fmt.Errorf("the vocabulary must contain %s, %s, %s and %s", unk, cls, sep, pad)
	}
	return t, nil
}

// Load reads a vocab.txt: one token per line, the line number being the id.
func Load(path string) (*Tokenizer, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	vocab := map[string]int64{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var id int64
	for scanner.Scan() {
		vocab[strings.TrimRight(scanner.Text(), "\r\n")] = id
		id++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return New(vocab)
}

// Encode returns [CLS] tokens [SEP] as ids, at most maxLen long (the [SEP] is kept
// when text is cut).
func (t *Tokenizer) Encode(text string, maxLen int) []int64 {
	if maxLen < 3 {
		maxLen = 3
	}
	ids := []int64{t.ClsID}
	for _, word := range basicTokens(text) {
		for _, piece := range t.wordPieces(word) {
			ids = append(ids, piece)
		}
		if len(ids) >= maxLen-1 {
			break
		}
	}
	if len(ids) > maxLen-1 {
		ids = ids[:maxLen-1]
	}
	return append(ids, t.SepID)
}

// wordPieces is BERT's WordpieceTokenizer for one word.
func (t *Tokenizer) wordPieces(word string) []int64 {
	runes := []rune(word)
	if len(runes) > maxWordLength {
		return []int64{t.UnkID}
	}
	var pieces []int64
	for start := 0; start < len(runes); {
		end := len(runes)
		var found int64 = -1
		for end > start {
			piece := string(runes[start:end])
			if start > 0 {
				piece = "##" + piece
			}
			if id, ok := t.vocab[piece]; ok {
				found = id
				break
			}
			end--
		}
		if found < 0 {
			// One unmatched piece makes the whole word unknown, as in BERT.
			return []int64{t.UnkID}
		}
		pieces = append(pieces, found)
		start = end
	}
	return pieces
}

// basicTokens is BERT's BasicTokenizer (uncased).
func basicTokens(text string) []string {
	var cleaned strings.Builder
	for _, r := range text {
		switch {
		case r == 0 || r == 0xFFFD || isControl(r):
			continue
		case isWhitespace(r):
			cleaned.WriteRune(' ')
		case isCJK(r):
			cleaned.WriteRune(' ')
			cleaned.WriteRune(r)
			cleaned.WriteRune(' ')
		default:
			cleaned.WriteRune(r)
		}
	}
	lowered := stripAccents(strings.ToLower(cleaned.String()))

	var tokens []string
	for _, word := range strings.Fields(lowered) {
		var current strings.Builder
		flush := func() {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		}
		for _, r := range word {
			if isPunctuation(r) {
				flush()
				tokens = append(tokens, string(r))
			} else {
				current.WriteRune(r)
			}
		}
		flush()
	}
	return tokens
}

func stripAccents(text string) string {
	var out strings.Builder
	for _, r := range norm.NFD.String(text) {
		if !unicode.Is(unicode.Mn, r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func isWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || unicode.Is(unicode.Zs, r)
}

func isControl(r rune) bool {
	if r == '\t' || r == '\n' || r == '\r' {
		return false
	}
	return unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r)
}

// isPunctuation follows BERT: every non-alphanumeric ASCII symbol counts as
// punctuation, as does anything in a Unicode punctuation category.
func isPunctuation(r rune) bool {
	if (r >= 33 && r <= 47) || (r >= 58 && r <= 64) || (r >= 91 && r <= 96) || (r >= 123 && r <= 126) {
		return true
	}
	return unicode.IsPunct(r)
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF) || (r >= 0x20000 && r <= 0x2A6DF) ||
		(r >= 0x2A700 && r <= 0x2B73F) || (r >= 0x2B740 && r <= 0x2B81F) || (r >= 0x2B820 && r <= 0x2CEAF) ||
		(r >= 0xF900 && r <= 0xFAFF) || (r >= 0x2F800 && r <= 0x2FA1F)
}
