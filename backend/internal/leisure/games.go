package leisure

import (
	"crypto/rand"
	"math/big"
	"sync"
)

func Result(board string) string {
	if len(board) != 9 {
		return "invalid"
	}
	for _, line := range [][3]int{{0, 1, 2}, {3, 4, 5}, {6, 7, 8}, {0, 3, 6}, {1, 4, 7}, {2, 5, 8}, {0, 4, 8}, {2, 4, 6}} {
		v := board[line[0]]
		if v != '.' && v == board[line[1]] && v == board[line[2]] {
			return string(v)
		}
	}
	for i := 0; i < 9; i++ {
		if board[i] == '.' {
			return ""
		}
	}
	return "draw"
}

var scores map[string]int
var once sync.Once

func score(board string, bot bool) int {
	key := board + "X"
	if bot {
		key = board + "O"
	}
	if v, ok := scores[key]; ok {
		return v
	}
	switch Result(board) {
	case "O":
		return 10
	case "X":
		return -10
	case "draw", "invalid":
		return 0
	}
	best := -100
	if !bot {
		best = 100
	}
	for i := 0; i < 9; i++ {
		if board[i] != '.' {
			continue
		}
		b := []byte(board)
		b[i] = 'X'
		if bot {
			b[i] = 'O'
		}
		v := score(string(b), !bot)
		if bot {
			best = max(best, v)
		} else {
			best = min(best, v)
		}
	}
	scores[key] = best
	return best
}

// PHP chooses uniformly among optimal moves. Build a read-only minimax map once.
func BotMove(board string) string {
	if Result(board) != "" {
		return board
	}
	once.Do(func() { scores = map[string]int{}; score(".........", false); score(".........", true) })
	best := -100
	moves := []int{}
	for i := 0; i < 9; i++ {
		if board[i] != '.' {
			continue
		}
		b := []byte(board)
		b[i] = 'O'
		v := 0
		switch Result(string(b)) {
		case "O":
			v = 10
		case "X":
			v = -10
		case "draw":
			v = 0
		default:
			v = scores[string(b)+"X"]
		}
		if v > best {
			best = v
			moves = []int{i}
		} else if v == best {
			moves = append(moves, i)
		}
	}
	if len(moves) == 0 {
		return board
	}
	n, e := rand.Int(rand.Reader, big.NewInt(int64(len(moves))))
	if e != nil {
		panic(e)
	}
	b := []byte(board)
	b[moves[n.Int64()]] = 'O'
	return string(b)
}
