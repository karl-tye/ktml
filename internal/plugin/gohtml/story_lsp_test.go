package gohtml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const lspStoryCard = `import { Payment } from "./payment.ktmlschema"

Card [payment: Payment, title: string, open: boolean = false]

Card [payment = Unpaid] { "{title}" }

Card [payment = Pending, reference] { "{reference}" }

Card [payment = Paid, amount, receipt] { a href="{receipt}" "{amount.display}" }
`

func storyLSPWorkspace(t *testing.T) (cardPath, storyPath string) {
	t.Helper()
	dir := t.TempDir()
	cardPath = filepath.Join(dir, "card.ktml")
	storyPath = filepath.Join(dir, "card.story.ktml")
	for path, source := range map[string]string{
		filepath.Join(dir, "payment.ktmlschema"): lspPaymentSchema,
		cardPath:                                 lspStoryCard,
		filepath.Join(dir, "ktml.config.json"):   "{}",
	} {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return cardPath, storyPath
}

func TestLSP_Story_GIVENATypeError_WHENOpened_THENPublishesItAtItsRange(t *testing.T) {
	_, storyPath := storyLSPWorkspace(t)
	source := "import { Card } from \"./card.ktml\"\n\nstory Card.Lost [payment = Lost, title = \"x\"]\n"
	response := openDocument(t, storyPath, source, 1)
	var notification struct {
		Params struct {
			Diagnostics []struct {
				Range lspRange `json:"range"`
				Code  string   `json:"code"`
			} `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(response, &notification); err != nil {
		t.Fatalf("decode %s: %v", response, err)
	}
	for _, diagnostic := range notification.Params.Diagnostics {
		if diagnostic.Code == "unknown-variant" && diagnostic.Range.spell(t, source) == "Lost" && diagnostic.Range.Start.Line == 2 {
			return
		}
	}
	t.Fatalf("diagnostics = %+v", notification.Params.Diagnostics)
}

func TestLSP_Story_GIVENACursorInAStory_WHENCompleted_THENOffersTypedInputsAndValues(t *testing.T) {
	_, storyPath := storyLSPWorkspace(t)
	header := "import { Card } from \"./card.ktml\"\n\n"
	tests := []struct {
		name, source, marker, want string
	}{
		{"components", header + "story ", "story ", "Card:7"},
		{"inputs", header + "story Card.A [", "Card.A [", "payment:6,title:6,open:6"},
		{"Variants", header + "story Card.A [payment = ", "payment = ", "Unpaid:20,Pending:20,Paid:20"},
		{"payload fields", header + "story Card.A [payment = Paid [", "Paid [", "amount:5,receipt:5"},
		{"booleans", header + "story Card.A [open = ", "open = ", "None:12,true:12,false:12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			openDocument(t, storyPath, tt.source, 1)
			response := callLSP(t, "textDocument/completion", map[string]any{
				"id": 1, "textDocument": map[string]any{"uri": "file://" + storyPath},
				"position": positionOf(t, tt.source, tt.marker, len(tt.marker)),
			})
			var decoded struct {
				Result struct {
					Items []struct {
						Label string `json:"label"`
						Kind  int    `json:"kind"`
					} `json:"items"`
				} `json:"result"`
			}
			if err := json.Unmarshal(response, &decoded); err != nil {
				t.Fatalf("decode %s: %v", response, err)
			}
			var got []string
			for _, item := range decoded.Result.Items {
				got = append(got, item.Label+":"+strconv.Itoa(item.Kind))
			}
			if joined := strings.Join(got, ","); joined != tt.want {
				t.Errorf("completion = %s, want %s (%s)", joined, tt.want, response)
			}
		})
	}
}

func TestLSP_Story_GIVENAStoryDocument_WHENSymbolsAndDefinitionsAreRequested_THENNamesStoriesAndReachesTheComponent(t *testing.T) {
	cardPath, storyPath := storyLSPWorkspace(t)
	source := "import { Card } from \"./card.ktml\"\n\nfixture link: TrustedURL = \"/x\"\n\nstory Card.Unpaid [payment = Unpaid, title = \"x\"]\n"
	if err := os.WriteFile(storyPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	openDocument(t, storyPath, source, 1)
	response := callLSP(t, "textDocument/documentSymbol", map[string]any{"id": 1, "textDocument": map[string]any{"uri": "file://" + storyPath}})
	if !strings.Contains(string(response), `"name":"Card.Unpaid"`) || !strings.Contains(string(response), `"name":"link"`) {
		t.Errorf("document symbols = %s", response)
	}
	definition := callLSP(t, "textDocument/definition", map[string]any{
		"id": 2, "textDocument": map[string]any{"uri": "file://" + storyPath},
		"position": positionOf(t, source, "Card.Unpaid", 1),
	})
	if !strings.Contains(string(definition), "file://"+cardPath) {
		t.Errorf("definition of the story's component = %s", definition)
	}
}
