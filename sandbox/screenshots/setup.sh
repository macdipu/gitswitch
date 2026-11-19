set -e
export HOME=/home/dana GITSWITCH_TEST_NO_SYSTEM=1
mkdir -p $HOME/projects /tmp/bin $HOME/origin
cd /src
go build -buildvcs=false -o /tmp/bin/gitswitch ./cmd/gitswitch
go build -buildvcs=false -o /tmp/bin/serve ./internal/zzdevserve
export PATH=/tmp/bin:$PATH
. /shots/files.sh
git config --global init.defaultBranch main
git config --global merge.conflictstyle diff3
git config --global user.name "Dana Whitfield"; git config --global user.email dana@acme-corp.example
git config --global safe.directory '*'
mkdir -p ~/.ssh && ssh-keygen -q -t ed25519 -N '' -C dana@acme-corp.example -f ~/.ssh/id_dana_sign
echo "dana@acme-corp.example $(cat ~/.ssh/id_dana_sign.pub)" > ~/.ssh/allowed_signers
git config --global gpg.ssh.allowedSignersFile ~/.ssh/allowed_signers
gitswitch account add work --email dana@acme-corp.example --name "Dana Whitfield" --key new >/dev/null
gitswitch account edit work --signing-format ssh --signing-key ~/.ssh/id_dana_sign.pub >/dev/null
gitswitch account add personal --email dana.whitfield@mail.example --name "Dana Whitfield" --key new >/dev/null

# c DAYS HHMM WHO "msg" [S]
c() {
  local when; when="$(date -d "$1 days ago $2" '+%Y-%m-%dT%H:%M:%S')"
  case $3 in D) N="Dana Whitfield"; E=dana@acme-corp.example;; M) N="Marcus Lee"; E=marcus@acme-corp.example;; P) N="Priya Nair"; E=priya@acme-corp.example;; X) N="Dana Whitfield"; E=dana.whitfield@mail.example;; esac
  git add -A
  GIT_AUTHOR_NAME="$N" GIT_AUTHOR_EMAIL=$E GIT_COMMITTER_NAME="$N" GIT_COMMITTER_EMAIL=$E GIT_AUTHOR_DATE=$when GIT_COMMITTER_DATE=$when \
   git ${5:+-c gpg.format=ssh -c user.signingkey=$HOME/.ssh/id_dana_sign.pub} commit -q ${5:+-S} -m "$4"
}

git init -q --bare $HOME/origin/storefront.git
mkdir -p $HOME/projects/storefront && cd $HOME/projects/storefront && git init -q
git remote add origin $HOME/origin/storefront.git
printf 'module github.com/acme-corp/storefront\n\ngo 1.25\n' > go.mod
printf '# Storefront\n\nThe Go API and React storefront for the shop.\n' > README.md
main_v1
c 40 1015 M "Start the Go API with a server skeleton"
cart_v1; c 38 1430 M "Add the cart package"
handler_v1; c 36 0920 P "Add checkout handler"
cart_v2; c 33 1605 M "Limit quantity per item"
checkout_v1; api_v1
w web/package.json <<'X'
{ "name": "storefront-web", "private": true, "scripts": { "dev": "vite", "build": "vite build" } }
X
c 31 1110 D "Add the web storefront with a checkout form" S
sed -i 's/go 1.25/go 1.26/' go.mod; c 29 1700 D "Bump go to 1.26"
cart_v3; c 27 1215 P "Add Remove to the cart"
printf '# Changelog\n\n## 1.3.0\n- Checkout form and card payments\n' > CHANGELOG.md
c 25 1000 D "Release 1.3.0" S; git tag -a v1.3.0 -m "1.3.0"
cart_base; c 23 1355 D "Add discounts to the cart" S
git branch fix/cart-rounding
handler_v2; payment_v1; c 21 1040 M "Extract payment client"
checkout_v2; c 19 1520 D "Add checkout form validation" S
api_v2; c 17 0945 P "Handle expired sessions in api.ts"
cart_main; c 14 1130 M "Cap discounts at 50%"
sed -i 's/go 1.26/go 1.27/' go.mod; c 12 1645 D "Bump go to 1.27" S
api_v3; c 10 1010 P "Cache the product list in api.ts"
git switch -q -c release/1.4
printf '\n## 1.4.0\n- Discounts, validation, payment client\n' >> CHANGELOG.md
c 8 1400 D "Prepare release 1.4.0" S; git tag -a v1.4.0 -m "1.4.0"
printf 'The checkout API takes cartId, email and card.\n' > docs.md; c 6 1215 P "Document the checkout API"
git switch -q main
w internal/cart/cart_test.go <<'X'
package cart

import "testing"

func TestTotalAppliesDiscount(t *testing.T) {
	c := New("t")
	c.Add(Item{SKU: "mug", Price: 1250, Qty: 2})
	c.ApplyDiscount(10)
	if got := c.Total(); got != 2250 {
		t.Fatalf("Total() = %d, want 2250", got)
	}
}
X
c 9 0930 M "Add a cart discount test"
git push -q -u origin main release/1.4 --tags
# the feature branch: two shared commits
git switch -q -c feature/checkout
checkout_v3; c 5 1100 D "Show the order summary in checkout" S
printf 'export const EXPRESS = false; // flag for the one-click flow\n' > web/src/flags.ts; c 4 1530 D "Add a flag for express checkout"
git push -q -u origin feature/checkout
git switch -q main
w web/src/Shipping.tsx <<'X'
export function Shipping() {
  return <p>Free shipping on orders over $50.</p>;
}
X
c 3 1000 P "Add shipping note to the cart page"
git push -q origin main
# a teammate pushes to origin (the local branches then diverge)
git clone -q $HOME/origin/storefront.git /tmp/other && cd /tmp/other
git config user.name "Priya Nair"; git config user.email priya@acme-corp.example
git switch -q feature/checkout
printf '// express checkout copy\n' > web/src/express.ts; git add -A
GIT_AUTHOR_DATE="$(date -d '2 days ago 11:20' -Iseconds)" GIT_COMMITTER_DATE="$(date -d '2 days ago 11:20' -Iseconds)" git commit -q -m "Add express checkout copy"
git push -q origin feature/checkout
git switch -q -c fix/ci-cache main; printf 'cache: go\n' > ci-cache.yml; git add -A
GIT_AUTHOR_DATE="$(date -d '1 days ago 09:10' -Iseconds)" GIT_COMMITTER_DATE="$(date -d '1 days ago 09:10' -Iseconds)" git commit -q -m "Cache Go modules in CI"
git push -q origin fix/ci-cache
cd $HOME/projects/storefront && git switch -q feature/checkout
printf 'export type Cart = { id: string; items: number };\n' > web/src/types.ts; c 1 1445 D "Add a Cart type for the web app" S
sed -i 's/EXPRESS = false/EXPRESS = true/' web/src/flags.ts; c 0 0840 D "Turn on express checkout for staff" S
git fetch -q
# the fix branch
git switch -q fix/cart-rounding; cart_fix; c 11 1500 D "Fix cart total rounding on discounts" S
git switch -q feature/checkout
gitswitch repo add $HOME/projects/storefront >/dev/null
gitswitch use work -C $HOME/projects/storefront >/dev/null 2>&1 || (cd $HOME/projects/storefront && gitswitch use work >/dev/null)
# second repo
mkdir -p $HOME/projects/docs-site && cd $HOME/projects/docs-site && git init -q
echo "# Docs" > index.md; c 20 1000 X "Start the docs site"; printf '\n## Install\n' >> index.md; c 7 1000 X "Add install page"
gitswitch repo add $HOME/projects/docs-site >/dev/null
(cd $HOME/projects/docs-site && gitswitch use personal >/dev/null 2>&1 || true)
cd $HOME/projects/storefront
# parked / stash / discarded entries
git switch -q fix/cart-rounding
printf '\n// TODO: show the rounded discount line\n' >> internal/cart/cart.go; echo 'export const SHIP_FREE_OVER = 50;' > web/src/shipping.ts
gitswitch switch feature/checkout --park >/dev/null
git switch -q main 2>/dev/null; sed -i 's/Free shipping on orders over \$50/Free shipping on orders over $50 in the US/' web/src/Shipping.tsx
git stash push -q -m "Reword the shipping note"
printf 'scratch\n' >> README.md; gitswitch discard README.md -y >/dev/null
git switch -q feature/checkout
git add -A >/dev/null
# the uncommitted work
handler_wip; checkout_wip; format_ts
git add web/src/Checkout.tsx
git stash list; git status -sb
cd /; git config --global --list | grep -i "name\|email" >/dev/null
cp -a /home/dana /home/dana.bak
cd /src && exec serve
