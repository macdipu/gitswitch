# file content writers (sourced by setup.sh)
w() { mkdir -p "$(dirname "$1")"; cat > "$1"; }

cart_v1() { w internal/cart/cart.go <<'X'
// Package cart holds the shopping cart: items, quantities and discounts.
package cart

import (
	"errors"
	"fmt"
)

var ErrEmpty = errors.New("cart is empty")

// Item is one line in the cart. Prices are in cents.
type Item struct {
	SKU   string
	Name  string
	Price int64
	Qty   int
}

// Cart is what a shopper has picked so far.
type Cart struct {
	ID    string
	Items []Item
}

func New(id string) *Cart {
	return &Cart{ID: id}
}

// Add puts an item in the cart, merging with a line for the same SKU.
func (c *Cart) Add(it Item) {
	for i := range c.Items {
		if c.Items[i].SKU == it.SKU {
			c.Items[i].Qty += it.Qty
			return
		}
	}
	c.Items = append(c.Items, it)
}

// Subtotal is the sum of every line before any discount.
func (c *Cart) Subtotal() int64 {
	var sum int64
	for _, it := range c.Items {
		sum += it.Price * int64(it.Qty)
	}
	return sum
}

// Total is what the shopper pays.
func (c *Cart) Total() int64 {
	return c.Subtotal()
}

// Format shows a price as dollars, like 12.50.
func Format(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}
X
}

# v2 (Marcus): quantity limits
cart_v2() { w internal/cart/cart.go <<'X'
// Package cart holds the shopping cart: items, quantities and discounts.
package cart

import (
	"errors"
	"fmt"
)

// MaxQty is the most of one product a single cart may hold.
const MaxQty = 20

var ErrEmpty = errors.New("cart is empty")
var ErrTooMany = errors.New("too many of one item")

// Item is one line in the cart. Prices are in cents.
type Item struct {
	SKU   string
	Name  string
	Price int64
	Qty   int
}

// Cart is what a shopper has picked so far.
type Cart struct {
	ID    string
	Items []Item
}

func New(id string) *Cart {
	return &Cart{ID: id}
}

// Add puts an item in the cart, merging with a line for the same SKU.
func (c *Cart) Add(it Item) error {
	for i := range c.Items {
		if c.Items[i].SKU == it.SKU {
			if c.Items[i].Qty+it.Qty > MaxQty {
				return ErrTooMany
			}
			c.Items[i].Qty += it.Qty
			return nil
		}
	}
	if it.Qty > MaxQty {
		return ErrTooMany
	}
	c.Items = append(c.Items, it)
	return nil
}

// Subtotal is the sum of every line before any discount.
func (c *Cart) Subtotal() int64 {
	var sum int64
	for _, it := range c.Items {
		sum += it.Price * int64(it.Qty)
	}
	return sum
}

// Total is what the shopper pays.
func (c *Cart) Total() int64 {
	return c.Subtotal()
}

// Format shows a price as dollars, like 12.50.
func Format(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}
X
}

# v3 (Priya): Remove
cart_v3() { perl -0pi -e 's{// Subtotal is}{// Remove drops the line for sku, if there is one.\nfunc (c *Cart) Remove(sku string) {\n\tfor i := range c.Items {\n\t\tif c.Items[i].SKU == sku {\n\t\t\tc.Items = append(c.Items[:i], c.Items[i+1:]...)\n\t\t\treturn\n\t\t}\n\t}\n}\n\n// Subtotal is}' internal/cart/cart.go; }

# base (Dana): discounts
cart_base() {
  perl -0pi -e 's{\tID    string\n\tItems \[\]Item\n}{\tID       string\n\tItems    []Item\n\tDiscount int // percent off the whole cart, 0 to 100\n}; s{// Total is what the shopper pays\.\nfunc \(c \*Cart\) Total\(\) int64 \{\n\treturn c.Subtotal\(\)\n\}}{// Total is what the shopper pays: the subtotal minus the discount.\nfunc (c *Cart) Total() int64 {\n\treturn c.Subtotal() - c.Subtotal()*int64(c.Discount)/100\n}\n\n// ApplyDiscount sets a percentage off the whole cart.\nfunc (c *Cart) ApplyDiscount(pct int) error {\n\tif pct < 0 || pct > 100 {\n\t\treturn errors.New("discount must be between 0 and 100")\n\t}\n\tc.Discount = pct\n\treturn nil\n}}' internal/cart/cart.go; }

# Marcus on main: cap discounts, clamp totals, currency sign (conflicts with the rounding fix)
cart_main() {
  perl -0pi -e 's{const MaxQty = 20\n}{const MaxQty = 20\n\n// MaxDiscount is the biggest percentage the shop will give away.\nconst MaxDiscount = 50\n}; s{\treturn c.Subtotal\(\) - c.Subtotal\(\)\*int64\(c.Discount\)/100\n}{\treturn max(0, c.Subtotal()-c.Subtotal()*int64(c.Discount)/100)\n}; s{if pct < 0 \|\| pct > 100 \{}{if pct < 0 || pct > MaxDiscount \{}; s{discount must be between 0 and 100}{discount must be between 0 and 50}; s{return fmt.Sprintf\("%d.%02d", cents/100, cents%100\)}{return fmt.Sprintf("\$%d.%02d", cents/100, cents%100)}' internal/cart/cart.go; }

# Dana on fix/cart-rounding
cart_fix() {
  perl -0pi -e 's{\treturn c.Subtotal\(\) - c.Subtotal\(\)\*int64\(c.Discount\)/100\n}{\treturn c.Subtotal() - (c.Subtotal()*int64(c.Discount)+50)/100\n}; s{if pct < 0 \|\| pct > 100 \{}{if pct < 0 || pct >= 100 \{}; s{return fmt.Sprintf\("%d.%02d", cents/100, cents%100\)}{return fmt.Sprintf("%d.%02d", cents/100, abs(cents%100))}; s{// Format shows}{func abs(n int64) int64 {\n\tif n < 0 {\n\t\treturn -n\n\t}\n\treturn n\n}\n\n// Format shows}' internal/cart/cart.go; }

main_v1() { w cmd/server/main.go <<'X'
package main

import (
	"log"
	"net/http"

	"github.com/acme-corp/storefront/internal/cart"
	"github.com/acme-corp/storefront/internal/checkout"
)

func main() {
	carts := map[string]*cart.Cart{}
	mux := http.NewServeMux()
	mux.Handle("POST /api/checkout", checkout.Handler(carts))

	log.Println("storefront API listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
X
}

handler_v1() { w internal/checkout/handler.go <<'X'
// Package checkout turns a cart into an order.
package checkout

import (
	"encoding/json"
	"net/http"

	"github.com/acme-corp/storefront/internal/cart"
)

type request struct {
	CartID string `json:"cartId"`
	Email  string `json:"email"`
	Card   string `json:"card"`
}

type response struct {
	OrderID string `json:"orderId"`
	Total   string `json:"total"`
}

// Handler charges the cart and returns the order number.
func Handler(carts map[string]*cart.Cart) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		c, ok := carts[req.CartID]
		if !ok || len(c.Items) == 0 {
			http.Error(w, "cart is empty", http.StatusUnprocessableEntity)
			return
		}
		json.NewEncoder(w).Encode(response{OrderID: "A-1001", Total: cart.Format(c.Total())})
	}
}
X
}

handler_v2() { w internal/checkout/handler.go <<'X'
// Package checkout turns a cart into an order.
package checkout

import (
	"encoding/json"
	"net/http"

	"github.com/acme-corp/storefront/internal/cart"
	"github.com/acme-corp/storefront/internal/payment"
)

type request struct {
	CartID string `json:"cartId"`
	Email  string `json:"email"`
	Card   string `json:"card"`
}

type response struct {
	OrderID string `json:"orderId"`
	Total   string `json:"total"`
}

// Handler charges a cart through the payment provider.
type Handler struct {
	Carts   map[string]*cart.Cart
	Payment *payment.Client
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "that request could not be read", http.StatusBadRequest)
		return
	}
	c, ok := h.Carts[req.CartID]
	if !ok || len(c.Items) == 0 {
		http.Error(w, "your cart is empty", http.StatusUnprocessableEntity)
		return
	}
	if !validCard(req.Card) {
		http.Error(w, "that card number does not look right", http.StatusUnprocessableEntity)
		return
	}
	total := c.Total()
	charge, err := h.Payment.Charge(req.Card, total)
	if err != nil {
		http.Error(w, "the payment was declined", http.StatusPaymentRequired)
		return
	}
	res := response{OrderID: charge.ID, Total: cart.Format(total)}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// validCard checks the length and the Luhn sum of a card number.
func validCard(n string) bool {
	if len(n) < 12 || len(n) > 19 {
		return false
	}
	sum, alt := 0, false
	for i := len(n) - 1; i >= 0; i-- {
		d := int(n[i] - '0')
		if d < 0 || d > 9 {
			return false
		}
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}
X
}

# the uncommitted edit: renames, an idempotency key, structured logs
handler_wip() { w internal/checkout/handler.go <<'X'
// Package checkout turns a cart into an order.
package checkout

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/acme-corp/storefront/internal/cart"
	"github.com/acme-corp/storefront/internal/payment"
)

type request struct {
	CartID string `json:"cartId"`
	Email  string `json:"email"`
	Card   string `json:"card"`
}

type response struct {
	OrderID string `json:"orderId"`
	Total   string `json:"total"`
}

// Handler charges a cart through the payment provider.
type Handler struct {
	Carts   map[string]*cart.Cart
	Payment *payment.Client
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var in request
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "that request could not be read", http.StatusBadRequest)
		return
	}
	basket, ok := h.Carts[in.CartID]
	if !ok || len(basket.Items) == 0 {
		http.Error(w, "your cart is empty", http.StatusUnprocessableEntity)
		return
	}
	if !validCard(in.Card) {
		http.Error(w, "that card number does not look right", http.StatusUnprocessableEntity)
		return
	}
	amountCents := basket.Total()
	key := r.Header.Get("Idempotency-Key")
	charge, err := h.Payment.Charge(in.Card, amountCents, key)
	if err != nil {
		slog.Warn("payment declined", "cart", in.CartID, "err", err)
		http.Error(w, "the payment was declined", http.StatusPaymentRequired)
		return
	}
	out := response{OrderID: charge.ID, Total: cart.Format(amountCents)}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// validCard checks the length and the Luhn sum of a card number.
func validCard(n string) bool {
	if len(n) < 12 || len(n) > 19 {
		return false
	}
	sum, alt := 0, false
	for i := len(n) - 1; i >= 0; i-- {
		digit := int(n[i] - '0')
		if digit < 0 || digit > 9 {
			return false
		}
		if alt {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
		alt = !alt
	}
	return sum%10 == 0
}
X
}

payment_v1() { w internal/payment/client.go <<'X'
// Package payment talks to the card processor.
package payment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// Client is a small wrapper around the processor's charge API.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// Charge is what the processor sends back for a paid order.
type Charge struct {
	ID string `json:"id"`
}

// Charge takes amount (in cents) from the card.
func (c *Client) Charge(card string, amount int64) (*Charge, error) {
	body, _ := json.Marshal(map[string]any{"card": card, "amount": amount})
	req, err := http.NewRequest("POST", c.BaseURL+"/v1/charges", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("charge failed: %s", res.Status)
	}
	var out Charge
	return &out, json.NewDecoder(res.Body).Decode(&out)
}
X
}

checkout_v1() { w web/src/Checkout.tsx <<'X'
import { useState } from "react";
import { pay, type Order } from "./api";

type Props = { cartId: string; total: string; onDone: (o: Order) => void };

export function Checkout({ cartId, total, onDone }: Props) {
  const [email, setEmail] = useState("");
  const [card, setCard] = useState("");

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    onDone(await pay(cartId, email, card));
  }

  return (
    <form onSubmit={submit} className="checkout">
      <h2>Checkout</h2>
      <label>Email <input value={email} onChange={(e) => setEmail(e.target.value)} /></label>
      <label>Card number <input value={card} onChange={(e) => setCard(e.target.value)} /></label>
      <button type="submit">Pay ${total}</button>
    </form>
  );
}
X
}

checkout_v2() { w web/src/Checkout.tsx <<'X'
import { useState } from "react";
import { pay, type Order } from "./api";

type Props = { cartId: string; total: string; onDone: (o: Order) => void };

export function Checkout({ cartId, total, onDone }: Props) {
  const [email, setEmail] = useState("");
  const [card, setCard] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!/^\S+@\S+\.\S+$/.test(email)) {
      setError("Enter a valid email address.");
      return;
    }
    if (card.replace(/\s/g, "").length < 12) {
      setError("That card number looks too short.");
      return;
    }
    setBusy(true);
    try {
      onDone(await pay(cartId, email, card));
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="checkout">
      <h2>Checkout</h2>
      <label>Email <input value={email} onChange={(e) => setEmail(e.target.value)} /></label>
      <label>Card number <input value={card} onChange={(e) => setCard(e.target.value)} /></label>
      {error && <p role="alert" className="error">{error}</p>}
      <button type="submit" disabled={busy}>Pay ${total}</button>
    </form>
  );
}
X
}

checkout_v3() {
  sed -i 's/type Props = { cartId: string; total: string;/type Props = { cartId: string; items: number; total: string;/; s/{ cartId, total, onDone }/{ cartId, items, total, onDone }/' web/src/Checkout.tsx
  sed -i '/^      <h2>Checkout<\/h2>/a\      <p className="summary">{items} items, total ${total}</p>' web/src/Checkout.tsx
}

checkout_wip() { w web/src/Checkout.tsx <<'X'
import { useState } from "react";
import { pay, type Order } from "./api";
import { formatCard } from "./format";

type Props = { cartId: string; items: number; total: string; onDone: (o: Order) => void };

export function Checkout({ cartId, items, total, onDone }: Props) {
  const [email, setEmail] = useState("");
  const [card, setCard] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!/^\S+@\S+\.\S+$/.test(email)) {
      setError("Enter a valid email address.");
      return;
    }
    if (card.replace(/\s/g, "").length < 12) {
      setError("That card number looks too short.");
      return;
    }
    setSubmitting(true);
    try {
      onDone(await pay(cartId, email, card.replace(/\s/g, "")));
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="checkout">
      <h2>Checkout</h2>
      <p className="summary">{items} {items === 1 ? "item" : "items"}, total ${total}</p>
      <label>Email <input value={email} onChange={(e) => setEmail(e.target.value)} /></label>
      <label>Card number <input value={card} onChange={(e) => setCard(formatCard(e.target.value))} inputMode="numeric" /></label>
      {error && <p role="alert" className="error">{error}</p>}
      <button type="submit" disabled={submitting}>{submitting ? "Paying…" : `Pay $${total}`}</button>
    </form>
  );
}
X
}

format_ts() { w web/src/format.ts <<'X'
/** Groups a card number in fours as the shopper types: 4242 4242 4242 4242. */
export function formatCard(raw: string): string {
  const digits = raw.replace(/\D/g, "").slice(0, 19);
  return digits.replace(/(\d{4})(?=\d)/g, "$1 ");
}

/** Cents to a price string, like 12.50. */
export function money(cents: number): string {
  return (cents / 100).toFixed(2);
}
X
}

api_v1() { w web/src/api.ts <<'X'
export type Order = { orderId: string; total: string };

const BASE = "/api";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json() as Promise<T>;
}

export function pay(cartId: string, email: string, card: string): Promise<Order> {
  return request<Order>("/checkout", {
    method: "POST",
    body: JSON.stringify({ cartId, email, card }),
  });
}
X
}

api_v2() { perl -0pi -e 's{  if \(!res.ok\) throw new Error\(await res.text\(\)\);}{  if (res.status === 401) {\n    // the session ran out: sign in again, then come back here\n    window.location.assign("/login?next=" + encodeURIComponent(window.location.pathname));\n    throw new Error("Your session expired. Please sign in again.");\n  }\n  if (!res.ok) throw new Error(await res.text());}' web/src/api.ts; }

api_v3() { cat >> web/src/api.ts <<'X'

export type Product = { sku: string; name: string; price: number };

let products: Promise<Product[]> | null = null;

/** The product list rarely changes, so fetch it once per page load. */
export function listProducts(): Promise<Product[]> {
  products ??= request<Product[]>("/products");
  return products;
}
X
}
