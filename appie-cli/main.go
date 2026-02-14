package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	appie "github.com/gwillem/appie-go"
)

const defaultConfigPath = ".appie.json"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	configPath := defaultConfigPath
	if v := os.Getenv("APPIE_CONFIG"); v != "" {
		configPath = v
	}

	ctx := context.Background()
	cmd := os.Args[1]

	switch cmd {
	case "login":
		client := appie.New(appie.WithConfigPath(configPath))
		loginURL := client.LoginURL()

		// Start local server to catch the code
		codeCh := make(chan string, 1)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fatal("Could not start local server: %v", err)
		}
		port := listener.Addr().(*net.TCPAddr).Port

		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, loginPage, loginURL, port)
		})
		mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
			code := r.URL.Query().Get("code")
			if code == "" {
				http.Error(w, "Missing code", 400)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, successPage)
			codeCh <- code
		})

		srv := &http.Server{Handler: mux}
		go srv.Serve(listener)

		fmt.Fprintf(os.Stderr, "Login server running on http://127.0.0.1:%d\n", port)
		fmt.Fprintf(os.Stderr, "Send this URL to the user: http://127.0.0.1:%d\n", port)
		fmt.Fprintf(os.Stderr, "Waiting for login...\n")

		// Also output machine-readable JSON
		fmt.Printf(`{"login_url": "http://127.0.0.1:%d", "status": "waiting"}`+"\n", port)

		// Wait for code with timeout
		select {
		case code := <-codeCh:
			srv.Shutdown(ctx)
			if err := client.ExchangeCode(ctx, code); err != nil {
				fatal("Exchange failed: %v", err)
			}
			if err := client.SaveConfig(); err != nil {
				fatal("Save config failed: %v", err)
			}
			fmt.Println(`{"ok": true, "message": "Login successful"}`)
		case <-time.After(5 * time.Minute):
			srv.Shutdown(ctx)
			fatal("Login timed out after 5 minutes")
		}

	case "login-url":
		client := appie.New(appie.WithConfigPath(configPath))
		fmt.Println(client.LoginURL())

	case "exchange-code":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: appie-cli exchange-code <code>")
			fmt.Fprintln(os.Stderr, "       appie-cli exchange-code \"appie://login-exit?code=XXXXX\"")
			os.Exit(1)
		}
		client := appie.New(appie.WithConfigPath(configPath))
		code := extractCode(os.Args[2])
		if err := client.ExchangeCode(ctx, code); err != nil {
			fatal("Exchange failed: %v", err)
		}
		if err := client.SaveConfig(); err != nil {
			fatal("Save config failed: %v", err)
		}
		fmt.Println(`{"ok": true, "message": "Login successful"}`)

	case "member":
		client := mustAuth(ctx, configPath)
		member, err := client.GetMember(ctx)
		if err != nil {
			fatal("Get member failed: %v", err)
		}
		printJSON(member)

	case "search":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: appie-cli search <query> [limit]")
			os.Exit(1)
		}
		client := mustAnon(ctx, configPath)
		limit := 10
		if len(os.Args) >= 4 {
			limit, _ = strconv.Atoi(os.Args[3])
		}
		products, err := client.SearchProducts(ctx, os.Args[2], limit)
		if err != nil {
			fatal("Search failed: %v", err)
		}
		printJSON(products)

	case "product":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: appie-cli product <id>")
			os.Exit(1)
		}
		client := mustAnon(ctx, configPath)
		id, _ := strconv.Atoi(os.Args[2])
		product, err := client.GetProduct(ctx, id)
		if err != nil {
			fatal("Get product failed: %v", err)
		}
		printJSON(product)

	case "bonus":
		client := mustAnon(ctx, configPath)
		products, err := client.GetSpotlightBonusProducts(ctx)
		if err != nil {
			fatal("Get bonus failed: %v", err)
		}
		printJSON(products)

	case "receipts":
		client := mustAuth(ctx, configPath)
		receipts, err := client.GetReceipts(ctx)
		if err != nil {
			fatal("Get receipts failed: %v", err)
		}
		printJSON(receipts)

	case "receipt":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: appie-cli receipt <transaction-id>")
			os.Exit(1)
		}
		client := mustAuth(ctx, configPath)
		receipt, err := client.GetReceipt(ctx, os.Args[2])
		if err != nil {
			fatal("Get receipt failed: %v", err)
		}
		printJSON(receipt)

	case "shopping-list":
		client := mustAuth(ctx, configPath)
		list, err := client.GetShoppingList(ctx)
		if err != nil {
			fatal("Get shopping list failed: %v", err)
		}
		printJSON(list)

	case "shopping-lists":
		client := mustAuth(ctx, configPath)
		lists, err := client.GetShoppingLists(ctx, 0)
		if err != nil {
			fatal("Get shopping lists failed: %v", err)
		}
		printJSON(lists)

	case "add-to-list":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: appie-cli add-to-list <product-id> [quantity]")
			fmt.Fprintln(os.Stderr, "       appie-cli add-to-list --text \"free text item\" [quantity]")
			os.Exit(1)
		}
		client := mustAuth(ctx, configPath)
		if os.Args[2] == "--text" {
			if len(os.Args) < 4 {
				fatal("Missing text")
			}
			qty := 1
			if len(os.Args) >= 5 {
				qty, _ = strconv.Atoi(os.Args[4])
			}
			if err := client.AddFreeTextToShoppingList(ctx, os.Args[3], qty); err != nil {
				fatal("Add to list failed: %v", err)
			}
		} else {
			id, _ := strconv.Atoi(os.Args[2])
			qty := 1
			if len(os.Args) >= 4 {
				qty, _ = strconv.Atoi(os.Args[3])
			}
			if err := client.AddProductToShoppingList(ctx, id, qty); err != nil {
				fatal("Add to list failed: %v", err)
			}
		}
		fmt.Println(`{"ok": true}`)

	case "batch-add":
		// Reads JSON array from stdin: [{"id": 123, "qty": 2}, {"text": "free text", "qty": 1}]
		client := mustAuth(ctx, configPath)
		var batchItems []struct {
			ID   int    `json:"id"`
			Text string `json:"text"`
			Qty  int    `json:"qty"`
		}
		if err := json.NewDecoder(os.Stdin).Decode(&batchItems); err != nil {
			fatal("Invalid JSON input: %v", err)
		}
		items := make([]appie.ListItem, 0, len(batchItems))
		for _, b := range batchItems {
			qty := b.Qty
			if qty < 1 {
				qty = 1
			}
			if b.Text != "" {
				items = append(items, appie.ListItem{Name: b.Text, Quantity: qty})
			} else if b.ID > 0 {
				items = append(items, appie.ListItem{ProductID: b.ID, Quantity: qty})
			}
		}
		if len(items) == 0 {
			fatal("No valid items in input")
		}
		if err := client.AddToShoppingList(ctx, items); err != nil {
			fatal("Batch add failed: %v", err)
		}
		fmt.Printf(`{"ok": true, "added": %d}`+"\n", len(items))

	case "clear-list":
		client := mustAuth(ctx, configPath)
		if err := client.ClearShoppingList(ctx); err != nil {
			fatal("Clear list failed: %v", err)
		}
		fmt.Println(`{"ok": true}`)

	case "order":
		client := mustAuth(ctx, configPath)
		order, err := client.GetOrder(ctx)
		if err != nil {
			fatal("Get order failed: %v", err)
		}
		printJSON(order)

	case "add-to-order":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: appie-cli add-to-order <product-id> [quantity]")
			os.Exit(1)
		}
		client := mustAuth(ctx, configPath)
		id, _ := strconv.Atoi(os.Args[2])
		qty := 1
		if len(os.Args) >= 4 {
			qty, _ = strconv.Atoi(os.Args[3])
		}
		if err := client.AddToOrder(ctx, []appie.OrderItem{{ProductID: id, Quantity: qty}}); err != nil {
			fatal("Add to order failed: %v", err)
		}
		fmt.Println(`{"ok": true}`)

	case "list-items":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: appie-cli list-items <list-id>")
			os.Exit(1)
		}
		client := mustAuth(ctx, configPath)
		listItems, err := getListItems(ctx, client, os.Args[2])
		if err != nil {
			fatal("Get list items failed: %v", err)
		}
		printJSON(listItems)

	case "previously-bought":
		client := mustAuth(ctx, configPath)
		size := 100
		page := 0
		if len(os.Args) >= 3 {
			size, _ = strconv.Atoi(os.Args[2])
		}
		if len(os.Args) >= 4 {
			page, _ = strconv.Atoi(os.Args[3])
		}
		products, total, err := getPreviouslyBought(ctx, client, size, page)
		if err != nil {
			fatal("Get previously bought failed: %v", err)
		}
		result := map[string]any{
			"products":      products,
			"totalElements": total,
			"page":          page,
			"size":          size,
		}
		printJSON(result)

	case "bonus-products":
		client := mustAuth(ctx, configPath)
		size := 50
		if len(os.Args) >= 3 {
			size, _ = strconv.Atoi(os.Args[2])
		}
		products, err := getBonusProducts(ctx, client, size)
		if err != nil {
			fatal("Get bonus products failed: %v", err)
		}
		printJSON(products)

	case "search-recipes":
		client := mustAnon(ctx, configPath)
		query := ""
		if len(os.Args) >= 3 {
			query = os.Args[2]
		}
		size := 10
		if len(os.Args) >= 4 {
			size, _ = strconv.Atoi(os.Args[3])
		}
		recipes, err := searchRecipes(ctx, client, query, size)
		if err != nil {
			fatal("Search recipes failed: %v", err)
		}
		printJSON(recipes)

	case "recipe":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: appie-cli recipe <id>")
			os.Exit(1)
		}
		client := mustAnon(ctx, configPath)
		recipeID, _ := strconv.Atoi(os.Args[2])
		recipe, err := getRecipe(ctx, client, recipeID)
		if err != nil {
			fatal("Get recipe failed: %v", err)
		}
		printJSON(recipe)

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	cmds := []string{
		"login                  Login via local web page (easiest)",
		"login-url              Get the AH login URL (manual)",
		"exchange-code <code>   Exchange auth code or appie:// URL for tokens",
		"member                 Show member profile",
		"search <query> [n]     Search products",
		"product <id>           Get product details",
		"bonus                  Get spotlight bonus products",
		"receipts               List receipts (kassabonnen)",
		"receipt <id>           Get receipt details",
		"shopping-list          Show shopping list",
		"shopping-lists         List all shopping lists",
		"add-to-list <id> [qty] Add product to shopping list",
		"add-to-list --text \"item\" [qty]  Add free text item",
		"batch-add              Add multiple items from stdin (JSON array)",
		"clear-list             Clear shopping list",
		"order                  Show current order",
		"add-to-order <id> [qty] Add product to order",
		"search-recipes [query] [n] Search Allerhande recipes",
		"recipe <id>            Get recipe with ingredients",
	}
	fmt.Fprintln(os.Stderr, "Usage: appie-cli <command> [args]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	for _, c := range cmds {
		fmt.Fprintf(os.Stderr, "  %s\n", c)
	}
}

func mustAuth(ctx context.Context, configPath string) *appie.Client {
	client, err := appie.NewWithConfig(configPath)
	if err != nil {
		// Try loading and refreshing
		client = appie.New(appie.WithConfigPath(configPath))
		if err := client.LoadConfig(); err != nil {
			fatal("Not authenticated. Run: appie-cli login-url")
		}
	}
	if !client.IsAuthenticated() {
		fatal("Not authenticated. Run: appie-cli login-url")
	}
	return client
}

func mustAnon(ctx context.Context, configPath string) *appie.Client {
	// Try authenticated first, fall back to anonymous
	client, err := appie.NewWithConfig(configPath)
	if err == nil && client.IsAuthenticated() {
		return client
	}
	client = appie.New()
	if err := client.GetAnonymousToken(ctx); err != nil {
		fatal("Get anonymous token failed: %v", err)
	}
	return client
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// getListItems fetches items for a specific list via REST API
func getListItems(ctx context.Context, client *appie.Client, listID string) (json.RawMessage, error) {
	// We need to make a direct HTTP call since the library doesn't expose this
	url := fmt.Sprintf("https://api.ah.nl/mobile-services/lists/v3/lists/%s/items", listID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+client.AccessToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-client-name", "appie-ios")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API error: %d %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// getPreviouslyBought fetches previously bought products via GraphQL
func getPreviouslyBought(ctx context.Context, client *appie.Client, size, page int) (json.RawMessage, int, error) {
	query := fmt.Sprintf(`{ productSearch(input: { query: "" previouslyBought: true size: %d page: %d }) { products { id title brand category } page { totalElements totalPages } } }`, size, page)

	reqBody, _ := json.Marshal(map[string]string{"query": query})
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.ah.nl/graphql", bytes.NewReader(reqBody))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+client.AccessToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-client-name", "appie-ios")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Data struct {
			ProductSearch struct {
				Products json.RawMessage `json:"products"`
				Page     struct {
					TotalElements int `json:"totalElements"`
				} `json:"page"`
			} `json:"productSearch"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, 0, fmt.Errorf("parse error: %w", err)
	}
	return result.Data.ProductSearch.Products, result.Data.ProductSearch.Page.TotalElements, nil
}

// getBonusProducts fetches current bonus products via REST
func getBonusProducts(ctx context.Context, client *appie.Client, size int) (json.RawMessage, error) {
	url := fmt.Sprintf("https://api.ah.nl/mobile-services/product/search/v2?bonus=true&size=%d&sortOn=RELEVANCE", size)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+client.AccessToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-client-name", "appie-ios")
	req.Header.Set("x-application", "AHWEBSHOP")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API error: %d %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// extractCode extracts the authorization code from either a bare code or a full appie:// URL
func extractCode(input string) string {
	input = strings.TrimSpace(input)
	if strings.Contains(input, "code=") {
		u, err := url.Parse(input)
		if err == nil {
			if c := u.Query().Get("code"); c != "" {
				return c
			}
		}
		// Fallback: extract code= from the string manually
		parts := strings.SplitN(input, "code=", 2)
		if len(parts) == 2 {
			code := parts[1]
			if idx := strings.Index(code, "&"); idx >= 0 {
				code = code[:idx]
			}
			return code
		}
	}
	return input
}

const loginPage = `<!DOCTYPE html>
<html lang="nl">
<head>
<meta charset="utf-8">
<title>Albert Heijn Login</title>
<style>
  body { font-family: -apple-system, system-ui, sans-serif; max-width: 600px; margin: 60px auto; padding: 0 20px; background: #f5f5f5; }
  .card { background: white; border-radius: 12px; padding: 32px; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
  h1 { color: #00811c; margin-top: 0; }
  a.btn { display: inline-block; background: #00811c; color: white; padding: 12px 24px; border-radius: 8px; text-decoration: none; font-size: 16px; margin: 16px 0; }
  a.btn:hover { background: #006b17; }
  .step { margin: 16px 0; padding: 12px; background: #f9f9f9; border-radius: 8px; }
  .step-num { font-weight: bold; color: #00811c; }
  input { width: 100%%; padding: 10px; border: 2px solid #ddd; border-radius: 8px; font-size: 14px; box-sizing: border-box; margin: 8px 0; }
  button { background: #00811c; color: white; padding: 10px 20px; border: none; border-radius: 8px; font-size: 14px; cursor: pointer; }
  button:hover { background: #006b17; }
  .manual { margin-top: 24px; padding-top: 24px; border-top: 1px solid #eee; }
</style>
</head>
<body>
<div class="card">
  <h1>Albert Heijn Login</h1>
  <div class="step">
    <span class="step-num">Stap 1:</span> Open de login pagina en log in met je AH account.
  </div>
  <a class="btn" href="%s" target="_blank">Inloggen bij Albert Heijn</a>
  <div class="step">
    <span class="step-num">Stap 2:</span> Na het inloggen krijg je een foutmelding (de pagina kan niet worden geopend). Dat is normaal.
    Kopieer de volledige URL uit je adresbalk. Die begint met <code>appie://login-exit?code=</code>
  </div>
  <div class="step">
    <span class="step-num">Stap 3:</span> Plak de URL hieronder:
  </div>
  <form id="codeForm">
    <input type="text" id="codeInput" placeholder="appie://login-exit?code=..." autofocus>
    <button type="submit">Inloggen</button>
  </form>
  <p id="status"></p>
</div>
<script>
document.getElementById('codeForm').addEventListener('submit', function(e) {
  e.preventDefault();
  var input = document.getElementById('codeInput').value.trim();
  var code = input;
  var match = input.match(/code=([^&]+)/);
  if (match) code = match[1];
  if (!code) { document.getElementById('status').textContent = 'Voer een code of URL in.'; return; }
  document.getElementById('status').textContent = 'Bezig met inloggen...';
  window.location.href = 'http://127.0.0.1:%d/callback?code=' + encodeURIComponent(code);
});
</script>
</body>
</html>`

const successPage = `<!DOCTYPE html>
<html lang="nl">
<head>
<meta charset="utf-8">
<title>Ingelogd!</title>
<style>
  body { font-family: -apple-system, system-ui, sans-serif; max-width: 600px; margin: 60px auto; padding: 0 20px; background: #f5f5f5; }
  .card { background: white; border-radius: 12px; padding: 32px; box-shadow: 0 2px 8px rgba(0,0,0,0.1); text-align: center; }
  h1 { color: #00811c; }
</style>
</head>
<body>
<div class="card">
  <h1>Ingelogd!</h1>
  <p>Je bent succesvol ingelogd bij Albert Heijn. Je kunt dit venster sluiten.</p>
</div>
</body>
</html>`

// graphqlQuery executes a GraphQL query and returns the raw response body
func graphqlQuery(ctx context.Context, client *appie.Client, query string) ([]byte, error) {
	reqBody, _ := json.Marshal(map[string]string{"query": query})
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.ah.nl/graphql", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+client.AccessToken())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-client-name", "appie-ios")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API error: %d %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// searchRecipes searches Allerhande recipes via GraphQL
func searchRecipes(ctx context.Context, client *appie.Client, query string, size int) (json.RawMessage, error) {
	gql := fmt.Sprintf(`{
		recipeSearch(query: { query: %q, size: %d }) {
			result {
				id
				title
				slug
				cookTime
				images {
					rendition { url }
				}
			}
			page { totalElements totalPages }
		}
	}`, query, size)

	body, err := graphqlQuery(ctx, client, gql)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data struct {
			RecipeSearch json.RawMessage `json:"recipeSearch"`
		} `json:"data"`
		Errors json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse error: %w\nraw: %s", err, string(body))
	}
	if result.Errors != nil {
		return nil, fmt.Errorf("GraphQL errors: %s", string(result.Errors))
	}
	return result.Data.RecipeSearch, nil
}

// getRecipe fetches a single recipe with full details via GraphQL
func getRecipe(ctx context.Context, client *appie.Client, id int) (json.RawMessage, error) {
	gql := fmt.Sprintf(`{
		recipe(id: %d) {
			id
			title
			slug
			description
			cookTime
			prepTime
			servings
			tags
			ingredients {
				text
				quantity
				name { singular plural }
				unit { singular plural }
			}
			steps {
				text
				index
			}
			nutritions {
				name
				value
				unit
			}
			images {
				rendition { url }
			}
		}
	}`, id)

	body, err := graphqlQuery(ctx, client, gql)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data struct {
			Recipe json.RawMessage `json:"recipe"`
		} `json:"data"`
		Errors json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse error: %w\nraw: %s", err, string(body))
	}
	if result.Errors != nil {
		return nil, fmt.Errorf("GraphQL errors: %s", string(result.Errors))
	}
	return result.Data.Recipe, nil
}

func fatal(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	// Remove newlines for clean JSON
	msg = strings.ReplaceAll(msg, "\n", " ")
	fmt.Fprintf(os.Stderr, `{"error": "%s"}`+"\n", msg)
	os.Exit(1)
}
