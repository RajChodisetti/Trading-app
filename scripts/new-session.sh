#!/usr/bin/env bash
set -euo pipefail

THEME="${1:-<fill theme>}"

mkdir -p docs/sessions
template="docs/sessions/session-template.md"
[[ -f "$template" ]] || { echo "missing $template"; exit 1; }

date_str="$(date +%Y-%m-%d)"
# find next sequential number for the day
maxn=0
for f in docs/sessions/session-"$date_str"-*.md; do
  [[ -e "$f" ]] || continue
  n="${f##*-}"; n="${n%.md}"
  [[ "$n" =~ ^[0-9]+$ ]] && (( n>maxn )) && maxn="$n"
done
n=$(printf "%02d" $((maxn+1)))
out="docs/sessions/session-$date_str-$n.md"

sed \
  -e "s/<!--SESSION_ID-->/$n/g" \
  -e "s/<!--DATE-->/$date_str/g" \
  -e "s/<!--THEME-->/$THEME/g" \
  "$template" > "$out"

# ensure TODO exists and add placeholders
mkdir -p docs
if [[ ! -f docs/TODO.md ]]; then
  cat > docs/TODO.md <<'EOF'
# TODO

## Now
- [ ] 

## Next
- [ ] 

## Later
- [ ] 

## Done
- [ ] 
EOF
fi

echo "- [ ] session-$date_str-$n: $THEME" >> docs/TODO.md

echo "Created $out and appended to docs/TODO.md"
