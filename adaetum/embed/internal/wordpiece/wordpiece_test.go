package wordpiece

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func tiny(t *testing.T) *Tokenizer {
	t.Helper()
	words := []string{"[PAD]", "[UNK]", "[CLS]", "[SEP]", "hello", ",", "world", "!", "un", "##aff", "##able", "cafe", "disk", "##s", "the", "node", "-", "a", "1"}
	vocab := map[string]int64{}
	for i, w := range words {
		vocab[w] = int64(i)
	}
	tok, err := New(vocab)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func ids(tok *Tokenizer, names ...string) []int64 {
	out := make([]int64, len(names))
	for i, n := range names {
		out[i] = tok.vocab[n]
	}
	return out
}

func TestPunctuationIsSplitAndTextLowercased(t *testing.T) {
	tok := tiny(t)
	got := tok.Encode("Hello, WORLD!", 32)
	want := ids(tok, "[CLS]", "hello", ",", "world", "!", "[SEP]")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAWordIsGreedilySplitIntoPieces(t *testing.T) {
	tok := tiny(t)
	got := tok.Encode("unaffable disks", 32)
	want := ids(tok, "[CLS]", "un", "##aff", "##able", "disk", "##s", "[SEP]")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestAccentsAreStrippedAndUnknownWordsBecomeUNK(t *testing.T) {
	tok := tiny(t)
	if got, want := tok.Encode("Café", 32), ids(tok, "[CLS]", "cafe", "[SEP]"); !reflect.DeepEqual(got, want) {
		t.Fatalf("accents: got %v want %v", got, want)
	}
	if got, want := tok.Encode("zzzz hello", 32), ids(tok, "[CLS]", "[UNK]", "hello", "[SEP]"); !reflect.DeepEqual(got, want) {
		t.Fatalf("unknown: got %v want %v", got, want)
	}
	// One unmatched piece makes the whole word unknown, not a partial match.
	if got, want := tok.Encode("unaffablez", 32), ids(tok, "[CLS]", "[UNK]", "[SEP]"); !reflect.DeepEqual(got, want) {
		t.Fatalf("partial: got %v want %v", got, want)
	}
}

func TestVeryLongWordsAreUnknown(t *testing.T) {
	tok := tiny(t)
	long := make([]byte, 120)
	for i := range long {
		long[i] = 'a'
	}
	if got, want := tok.Encode(string(long), 32), ids(tok, "[CLS]", "[UNK]", "[SEP]"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestCJKCharactersAreSeparateTokens(t *testing.T) {
	tok := tiny(t)
	got := tok.Encode("hello世界", 32)
	want := ids(tok, "[CLS]", "hello", "[UNK]", "[UNK]", "[SEP]")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestControlCharactersAndOddWhitespaceAreHarmless(t *testing.T) {
	tok := tiny(t)
	got := tok.Encode("hello\x00\t world\r\n", 32)
	want := ids(tok, "[CLS]", "hello", "world", "[SEP]")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestTruncationKeepsTheClosingSeparator(t *testing.T) {
	tok := tiny(t)
	got := tok.Encode("the node a node a node a node a node a", 6)
	if len(got) != 6 || got[0] != tok.ClsID || got[5] != tok.SepID {
		t.Fatalf("a cut text must still be [CLS] ... [SEP] within the limit: %v", got)
	}
	if got := tok.Encode("", 8); !reflect.DeepEqual(got, []int64{tok.ClsID, tok.SepID}) {
		t.Fatalf("empty text: %v", got)
	}
}

func TestAVocabularyWithoutSpecialTokensIsRefused(t *testing.T) {
	if _, err := New(map[string]int64{"hello": 0}); err == nil {
		t.Fatal("a vocabulary missing [CLS] would produce vectors that are silently wrong")
	}
}

// The real check: ids from the actual bert-base-uncased vocabulary (which MiniLM-L6-v2
// uses). Run with EMBED_MODEL_DIR pointing at a directory holding vocab.txt.
func TestRealVocabularyMatchesKnownBERTIds(t *testing.T) {
	dir := os.Getenv("EMBED_MODEL_DIR")
	if dir == "" {
		t.Skip("set EMBED_MODEL_DIR to a directory with vocab.txt to check against the real vocabulary")
	}
	tok, err := Load(filepath.Join(dir, "vocab.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// Ids that are certain: bert-base-uncased has hello=7592, world=2088, ","=1010 and
	// "!"=999, with [CLS]=101 and [SEP]=102.
	cases := map[string][]int64{
		"hello world":   {101, 7592, 2088, 102},
		"Hello, World!": {101, 7592, 1010, 2088, 999, 102},
	}
	// Common whole words are single tokens whose id is their line in vocab.txt.
	var sentence []string
	want := []int64{101}
	for _, word := range []string{"the", "disk", "is", "full"} {
		id, ok := tok.vocab[word]
		if !ok {
			t.Fatalf("%q should be in the vocabulary", word)
		}
		sentence = append(sentence, word)
		want = append(want, id)
	}
	cases[strings.Join(sentence, " ")] = append(want, 102)
	for text, want := range cases {
		if got := tok.Encode(text, 128); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v want %v", text, got, want)
		}
	}
}
