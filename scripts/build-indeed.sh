#!/bin/bash
# Build the Indeed (Agent Marketplace) page from source data
# Run this before deploy to regenerate www/indeed.html and www/personas/
#
# Usage: ./scripts/build-indeed.sh
# Source of truth: data/agents/roster.json + data/agents/*/

set -euo pipefail
cd "$(dirname "$0")/.."

echo "🦦 Building Agent Marketplace..."

# 1. Generate persona JSON files for ZIP downloads
echo "  Generating persona JSONs..."
mkdir -p www/personas
python3 -c "
import os, json, glob

agents_dir = 'data/agents'
personas_dir = 'www/personas'

count = 0
for role_dir in sorted(glob.glob(f'{agents_dir}/*/')):
    role_id = os.path.basename(role_dir.rstrip('/'))
    if role_id.startswith('_') or role_id.startswith('.'): continue
    if not os.path.exists(os.path.join(role_dir, 'SOUL.md')): continue
    
    persona = {}
    for key, fname in [('identity', 'IDENTITY.md'), ('soul', 'SOUL.md'), ('summary', 'IDENTITY_SUMMARY.md')]:
        fpath = os.path.join(role_dir, fname)
        if os.path.exists(fpath):
            with open(fpath) as f:
                persona[key] = f.read()
        else:
            persona[key] = ''
    
    with open(os.path.join(personas_dir, f'{role_id}.json'), 'w') as f:
        json.dump(persona, f)
    count += 1

print(f'  Generated {count} persona files')
"

# 2. Generate slim roster data for embedding in HTML
echo "  Building roster data..."
python3 -c "
import json

with open('data/agents/roster.json') as f:
    roster = json.load(f)

slim = []
for a in roster['agents']:
    slim.append({
        'id': a['role_id'],
        'name': a['display_name'],
        'pronouns': a.get('pronouns',''),
        'role': a.get('role_name',''),
        'emoji': a.get('emoji',''),
        'type': a.get('role_type','ic'),
        'cat': a.get('category',''),
        'sub': a.get('subcategory',''),
        'tag': a.get('tagline',''),
        'tier': a.get('model_recommendations',{}).get('tier',''),
        'model': a.get('model_recommendations',{}).get('best_overall',''),
        'pros': a.get('pros',[]),
        'cons': a.get('cons',[])
    })

with open('/tmp/roster_slim.json', 'w') as f:
    json.dump(slim, f)
print(f'  Prepared {len(slim)} agents for embedding')
"

# 3. Inject roster data into indeed.html template
# The template has a placeholder: const AGENTS = [];
# We replace it with the actual data
echo "  Injecting data into template..."
python3 -c "
import json

with open('/tmp/roster_slim.json') as f:
    data = f.read()

with open('www/indeed.html') as f:
    html = f.read()

# Replace the data injection point
# Look for the AGENTS array assignment
import re
pattern = r'const AGENTS = \[.*?\];'
match = re.search(pattern, html, flags=re.DOTALL)
if match:
    new_html = html[:match.start()] + f'const AGENTS = {data};' + html[match.end():]
else:
    new_html = html

if new_html != html:
    with open('www/indeed.html', 'w') as f:
        f.write(new_html)
    print('  Data injected successfully')
else:
    print('  Warning: Could not find AGENTS injection point (may already be populated)')
"

echo "✅ Agent Marketplace built successfully"
echo "   www/indeed.html ($(wc -c < www/indeed.html | tr -d ' ') bytes)"
echo "   www/personas/ ($(ls www/personas/ | wc -l | tr -d ' ') files)"
