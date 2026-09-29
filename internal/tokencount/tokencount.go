package tokencount

import "github.com/tiktoken-go/tokenizer/codec"

var cl100k = codec.NewCl100kBase()

func Count(s string) int {
	if s == "" {
		return 0
	}
	n, err := cl100k.Count(s)
	if err != nil {
		return len(s) / 4
	}
	return n
}
