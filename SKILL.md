# Albert Heijn Grocery Skill

Weekly meal planning, recipe discovery, and grocery list management for Albert Heijn (Netherlands).

## First-Time Setup

You need Go installed and the user must log in to their AH account. Follow these steps exactly.

### Step 1: Install Go (if not present)

Check if Go is installed:
```bash
go version
```

If not installed:
```bash
curl -L https://go.dev/dl/go1.23.6.linux-amd64.tar.gz -o /tmp/go.tar.gz
mkdir -p ~/.local
tar -C ~/.local -xzf /tmp/go.tar.gz
export PATH=$HOME/.local/go/bin:$HOME/go/bin:$PATH
```

Add to your shell profile so it persists:
```bash
echo 'export PATH=$HOME/.local/go/bin:$HOME/go/bin:$PATH' >> ~/.bashrc
```

### Step 2: Build appie-cli

From this skill's directory:
```bash
cd appie-cli
go build -o appie-cli .
mv appie-cli ~/go/bin/
```

Verify it works:
```bash
appie-cli search "kaas" 3
```

This should return product results (no login required for search).

### Step 3: User Login (requires human interaction!)

The user must log in via their browser. You cannot do this for them.

**Easiest method: local login page**

```bash
appie-cli login
```

This starts a local web page. Send the URL (http://127.0.0.1:PORT) to the user. The page guides them through login with 3 simple steps:
1. Click the login button to open AH login
2. Log in with their AH account
3. After the redirect fails (that's normal), paste the URL from their address bar into the form

The CLI automatically exchanges the code and saves tokens. Done.

**Manual method (fallback):**

If the local server doesn't work (e.g. remote environments), use the manual flow:

```bash
appie-cli login-url
```

Send the URL to the user. After login, the browser tries to redirect to `appie://login-exit?code=XXXXX` which fails. The user copies the full URL (or just the code) and sends it back. Then:

```bash
# Both work: full URL or just the code
appie-cli exchange-code "appie://login-exit?code=XXXXX"
appie-cli exchange-code XXXXX
```

Verify login:
```bash
appie-cli member
```

Tokens are saved to `.appie.json` and auto-refresh. The user only needs to do this once.

### Step 4: Configure preferences

```bash
cp config-template.json config.json
```

**Don't just fill this in yourself — have a conversation with the user.** Ask them:

1. **Basics:** "How many meals per week should I plan? And how many people am I cooking for?"
2. **Cooking:** "What's the max cooking time you want? 30 minutes? An hour?"
3. **Dislikes & allergies:** "Any ingredients you hate or can't eat?"
4. **Shopping habits:** "What day do you usually do groceries?"
5. **Special arrangements:** "Do you buy certain things elsewhere? For example, some people get their meat at a butcher instead of AH."
   - If yes: ask which items → add to `butcher_items` in config
   - These will appear on the shopping list as free text notes (e.g. "🥩 Slager: kipfilet") so the user knows to get them elsewhere
   - If no: leave `butcher_items` as an empty list `[]`
6. **Anything else:** "Is there anything else I should know about how you want your groceries handled?"
   - Some users want only organic products
   - Some want to minimize packaging
   - Some have a strict budget
   - Whatever they say, capture it in config and taste-profile

The point is: **every household is different.** Don't assume — ask.

### Step 4b: Set up weekly basics

```bash
cp weekly-basics-template.json weekly-basics.json
```

Ask the user what they buy every week (melk, brood, eieren, fruit, etc.) and what they buy om de week (schoonmaakmiddel, tandpasta, etc.). Search for each product to find the AH product ID:

```bash
appie-cli search "halfvolle melk" 3
```

Fill `weekly-basics.json` with the product IDs, names and quantities. This file is the single source of truth for recurring items. No AH shopping lists needed.

### Step 5: Show the user what AH knows about them

This is a great starting point — it's fun and a little confronting. Right after login, run:

```bash
appie-cli member
```

This returns AH's internal profile of the user via `customerProfileProperties`: age range, life stage, food profile, diet type, price segment, share of wallet, favorite shopping day, and more.

**Share the highlights with the user!** Something like: "Wist je dat Albert Heijn dit allemaal over je weet?" followed by the interesting bits (food profile, diet, shopping day, etc). It's a great conversation starter and it helps you understand the user immediately.

⚠️ Note: the member response contains personal data (name, email). Don't log or share the raw output — only share the profile insights with the user themselves.

### Step 6: Build taste profile

Then pull their full purchase history:
```bash
appie-cli previously-bought 100 0
appie-cli previously-bought 100 1
appie-cli previously-bought 100 2
```

Combine the member profile + purchase history to create `taste-profile.md` (from `taste-profile-template.md`) — a summary of what the user likes, buys often, their cooking style, and their preferred cuisines.

## PATH

Always ensure Go and appie-cli are in PATH before running commands:
```bash
export PATH=$HOME/.local/go/bin:$HOME/go/bin:$PATH
```

## Weekly Workflow

Run this weekly (ideally the day before the user's shopping day).

### 1. Gather Data
```bash
# Get current bonus products
appie-cli bonus-products 200

# Get user's purchase history (for matching)
appie-cli previously-bought 100 0
```

Also read `weekly-basics.json` to know what recurring items to add later.

### 2. Find Bonus Matches
Cross-reference bonus products with previously bought items (`isPreviouslyBought: true`). These are deals the user actually cares about.

### 3. Search Recipes
```bash
# Search recipes by keyword
appie-cli search-recipes "pasta" 20

# Or browse without query (returns popular recipes)
appie-cli search-recipes "" 20

# Get full recipe details with ingredients
appie-cli recipe <recipe-id>
```

Filter recipes by:
- Cooking time ≤ `max_cooking_time_minutes` from config
- No ingredients in `dislikes` or `allergies`
- Prefer recipes using current bonus ingredients
- Check `taste-profile.md` for cuisine preferences

### 4. Present Proposal to User
Send meal suggestions via chat. For each meal include:
- Recipe name + link + cooking time
- Which ingredients are on bonus 🏷️
- Which items to get at the butcher 🥩
- Any items they need to buy beyond their usual basics

**ALWAYS wait for the user to approve before touching the shopping list.**

### 5. Handle Feedback
- User approves → add to shopping list (step 6)
- User modifies → adjust and ask again
- User rejects → suggest alternatives
- Log everything in `meal-history.json`

### 6. Fill Shopping List

#### Product cache
Before searching for a product, check `product-cache.json` first. It maps product names to AH product IDs so you can skip repeated searches. After finding a new product ID via search, add it to the cache under the appropriate section (`basics` or `ingredients`).

Before adding items, do these checks:

#### Butcher check
Go through ALL ingredients for ALL approved meals. For each ingredient, check if it matches any item in `butcher_items` from `config.json`. If it matches, do NOT add the AH product — instead add a free text note:
```bash
appie-cli add-to-list --text "🥩 Slager: rundergehakt (voor bolognese)" 1
```
Common matches to watch for: kipfilet, kip, gehakt, rundergehakt, half-om-half gehakt, biefstuk, worst, etc. Check ALL meat/protein ingredients, not just obvious ones.

#### Deduplication & smart quantities
Multiple recipes may need the same ingredient (e.g. two recipes both need onions, or garlic). Before adding to the list:

1. **Collect all ingredients** across all approved meals into one combined list
2. **Group by ingredient type** (e.g. "gele uien" appearing in 2 recipes)
3. **Check if one package is enough:**
   - Most vegetables (1 onion, 1 head of garlic) are enough for 2 recipes
   - Canned goods: if 2 recipes each need tomatenblokjes, you need 2 blikken
   - Spices/herbs: 1 package always covers multiple recipes
4. **Check for larger/cheaper packages:**
   - Search for the same product with different sizes: `appie-cli search "tomatenblokjes" 5`
   - Compare unit prices (€/kg or €/L) — sometimes a bigger can is cheaper
   - If the user needs 2+ of something, check if a multi-pack or larger size exists
5. **Add with correct quantity** — don't add 2 separate items when 1 will do, and don't add just 1 when you need 2

Example:
- Recipe A needs uien, Recipe B needs uien → 1 netje gele uien is enough (add 1x)
- Recipe A needs tomatenblokjes, Recipe B needs tomatenblokjes → need 2 blikjes (add 2x, or find a bigger can)
- Recipe A needs knoflook, Recipe B needs knoflook → 1 knoflook is enough (add 1x)

#### Then add to list
Combine weekly basics (from `weekly-basics.json`) and meal ingredients into one `batch-add` call:

```bash
echo '[{"id": 54074, "qty": 1}, {"id": 197393, "qty": 1}, {"text": "Slager: kipfilet", "qty": 1}]' | appie-cli batch-add
```

Each item is either `{"id": <product-id>, "qty": N}` for AH products or `{"text": "description", "qty": N}` for free text (butcher items, notes). Include the basics from `weekly-basics.json` in the same batch. One call, everything at once.

#### Save the recipes
After filling the list, save the approved meals to `meal-history.json` with date, recipe name, ingredients, cooking time, and any notes. Also save rejected meals with the reason — this helps improve future suggestions.

## API Reference

### REST Endpoints (via appie-cli)
| Command | What it does | Auth needed? |
|---------|-------------|--------------|
| `search <query> [limit]` | Search products | No |
| `product <id>` | Product details | No |
| `bonus-products [limit]` | Current bonus deals | No |
| `previously-bought [size] [page]` | Purchase history | Yes |
| `shopping-list` | View shopping list | Yes |
| `shopping-lists` | List all lists | Yes |
| `list-items <list-id>` | Items in specific list | Yes |
| `add-to-list <id> [qty]` | Add product to list | Yes |
| `add-to-list --text "item"` | Add free text to list | Yes |
| `batch-add` | Add multiple items from stdin (JSON) | Yes |
| `clear-list` | Clear shopping list | Yes |
| `search-recipes [query] [limit]` | Search Allerhande recipes | No |
| `recipe <id>` | Recipe with full ingredients | No |
| `member` | Member profile | Yes |
| `receipts` | Purchase receipts (broken, 503) | Yes |
| `receipt <id>` | Receipt details (broken, 503) | Yes |

### GraphQL Discoveries

**Previously bought** (undocumented — returns full purchase history):
```graphql
{ productSearch(input: { query: "" previouslyBought: true size: 100 page: 0 }) {
    products { id title brand category }
    page { totalElements totalPages }
} }
```

**Member segmentation** (reveals AH's internal profiling):
```graphql
{ member {
    customerProfileAudiences
    customerProfileProperties { key value }
} }
```

**Bonus segments:**
```graphql
{ bonusSegments { id title } }
```

## Files
- `config.json` -- user preferences (copy from `config-template.json`)
- `weekly-basics.json` -- recurring grocery items with product IDs (copy from `weekly-basics-template.json`)
- `taste-profile.md` -- learned taste profile (copy from `taste-profile-template.md`)
- `meal-history.json` -- meal approvals/rejections (copy from `meal-history-template.json`)
- `product-cache.json` -- cached product IDs to skip repeated searches (copy from `product-cache-template.json`)
- `.appie.json` -- AH auth tokens (auto-created on login, DO NOT commit)

## Known Issues

- **Receipts endpoint is broken** — `appie-cli receipts` returns a 503 error (`appie-receipt-bff.ctp-checkout-and-receipts-prd: Name does not resolve`). This is an upstream issue in the AH API, not our bug. Tracked at [gwillem/appie-go#1](https://github.com/gwillem/appie-go/issues/1). Use `previously-bought` instead to learn about purchase history.

## Important Rules
- **NEVER** add items to the shopping list without user approval
- **Butcher items are critical** — check EVERY meat/protein ingredient against `butcher_items` in config. If it matches, NEVER add the AH product. Always use free text: `appie-cli add-to-list --text "🥩 Slager: kipfilet"`. Missing this is confusing for the user.
- Respect dislikes and allergies — no exceptions
- Prefer bonus items when suggesting meals
- Keep recipes practical: common ingredients, within cooking time limit
- The user only needs to log in once — tokens auto-refresh after that
- **When in doubt, ask the user.** It's better to ask than to assume.
