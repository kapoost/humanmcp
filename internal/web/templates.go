package web

const allTemplates = `
{{define "index.html"}}<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Author}} — humanMCP</title>
<style>{{template "css" .}}</style>
</head>
<body>
<div class="container">
{{template "header" .}}
{{if .IsOwner}}
<div class="owner-bar">
  <span>✎ owner mode active</span>
  <button class="btn btn-primary" onclick="openEditor()">+ new piece</button>
</div>
{{end}}
{{if .Pieces}}
<ul class="pieces">
{{range .Pieces}}
<li class="piece-item">
  <div class="piece-meta">
    <span class="type-badge">{{.Type}}</span>
    {{if ne (lower (print .Access)) "public"}}<span class="locked-badge">{{.Access}}</span>{{end}}
    <span>{{formatDate .Published}}</span>
  </div>
  <div class="piece-title"><a href="/p/{{.Slug}}">{{.Title}}</a>
  {{if $.IsOwner}}<button style="font-size:.75rem;margin-left:.5rem;padding:1px 6px;cursor:pointer;border:1px solid var(--border);border-radius:3px;background:var(--bg);color:var(--muted);" onclick="openEditor('{{.Slug}}')">edit</button>{{end}}
  </div>
  {{if .Description}}<div class="piece-desc">{{.Description}}</div>{{end}}
  {{if .Tags}}<div class="tags">{{range .Tags}}<span class="tag">#{{.}}</span>{{end}}</div>{{end}}
</li>
{{end}}
</ul>
{{else}}
<div class="empty">No content yet.{{if .IsOwner}} Click "+ new piece" to write your first poem.{{end}}</div>
{{end}}
{{if .IsOwner}}{{template "editor" .}}{{end}}
{{template "footer" .}}
</div>
</body></html>
{{end}}

{{define "piece.html"}}<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Piece.Title}} — {{.Author}}</title>
<style>{{template "css" .}}
.poem-body{font-family:var(--serif);font-size:1.1rem;line-height:2;white-space:pre-wrap;margin:2rem 0;}
.essay-body{font-size:1rem;line-height:1.85;margin:2rem 0;}
.piece-header{margin-bottom:1.5rem;padding-bottom:1rem;border-bottom:1px solid var(--border);}
.piece-type{font-size:.75rem;text-transform:uppercase;letter-spacing:.1em;color:var(--muted);margin-bottom:.5rem;}
.piece-h1{font-size:1.6rem;font-weight:500;line-height:1.3;margin-bottom:.4rem;font-family:var(--serif);}
.gate-box{background:var(--locked-bg);border:1px solid var(--locked);border-radius:6px;padding:1.25rem;margin:2rem 0;}
.gate-box h3{color:var(--locked);margin-bottom:.75rem;font-size:.95rem;}
.gate-box input[type=text]{width:100%;padding:.5rem;border:1px solid var(--border);border-radius:4px;background:var(--bg);color:var(--fg);margin-bottom:.5rem;font-size:1rem;}
.unlock-success{background:#e8f5e9;border:1px solid #4caf50;border-radius:6px;padding:.75rem 1rem;margin-bottom:1rem;color:#2e7d32;font-size:.85rem;}
.back{display:inline-block;margin-bottom:1.5rem;font-size:.85rem;color:var(--muted);}
</style>
</head>
<body>
<div class="container">
{{template "header-simple" .}}
<a href="/" class="back">← all pieces</a>
{{with .Piece}}
<div class="piece-header">
  <div class="piece-type">{{.Type}} · {{formatDate .Published}}</div>
  <h1 class="piece-h1">{{.Title}}</h1>
  {{if .Tags}}<div class="tags">{{range .Tags}}<span class="tag">#{{.}}</span>{{end}}</div>{{end}}
</div>
{{if $.Unlocked}}<div class="unlock-success">✓ Correct answer — content unlocked</div>{{end}}
{{if $.IsLocked}}
  {{if .Description}}<p style="color:var(--muted);margin-bottom:1.5rem;">{{.Description}}</p>{{end}}
  <div class="gate-box">
    <h3>🔒 This content requires {{.Access}} access</h3>
    {{if eq (print .Gate) "challenge"}}
      <p style="margin-bottom:.75rem;font-size:.9rem;">Answer the author's question to read this piece:</p>
      <p style="font-weight:500;margin-bottom:1rem;">{{.Challenge}}</p>
      {{if $.WrongAnswer}}<p style="color:#c0392b;font-size:.85rem;margin-bottom:.5rem;">✗ Wrong answer, try again.</p>{{end}}
      <form method="POST" action="/unlock/{{.Slug}}">
        <input type="text" name="answer" placeholder="Your answer..." autocomplete="off" autofocus>
        <button type="submit" class="btn btn-primary">Unlock</button>
      </form>
    {{else if eq (print .Gate) "payment"}}
      <p style="font-size:.9rem;">This piece costs <strong>{{.PriceSats}} sats</strong>. Payment support coming soon.</p>
    {{else}}
      <p style="font-size:.9rem;">Members-only. Contact the author for access.</p>
    {{end}}
  </div>
{{else}}
  <div class="{{if eq .Type "poem"}}poem-body{{else}}essay-body{{end}}">{{nl2br .Body}}</div>
{{end}}
{{end}}
{{template "footer" .}}
</div>
</body></html>
{{end}}

{{define "login.html"}}<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Owner Login</title>
<style>{{template "css" .}}</style>
</head>
<body>
<div class="container" style="max-width:400px;">
<div style="padding:3rem 0;">
<h1 style="font-size:1.2rem;margin-bottom:1.5rem;">Owner Login</h1>
{{if .}}{{with .Error}}<p style="color:#c0392b;margin-bottom:1rem;font-size:.9rem;">{{.}}</p>{{end}}{{end}}
<form method="POST" action="/login" style="display:grid;gap:.75rem;">
  <input type="password" name="token" placeholder="Edit token" autofocus style="padding:.5rem;border:1px solid var(--border);border-radius:4px;background:var(--bg);color:var(--fg);font-size:1rem;">
  <button type="submit" class="btn btn-primary">Login</button>
</form>
<p style="margin-top:1rem;font-size:.8rem;color:var(--muted);"><a href="/">← back</a></p>
</div>
</div>
</body></html>
{{end}}

{{define "css"}}
:root{--bg:#fdfcfa;--fg:#1a1a1a;--muted:#6b6b6b;--border:#e2e0db;--accent:#2a6496;--accent-light:#e8f1f8;--locked:#7a5c00;--locked-bg:#fef9ec;--tag-bg:#f0ede8;--tag-fg:#555;--max:660px;--serif:Georgia,'Times New Roman',serif;--sans:-apple-system,BlinkMacSystemFont,'Segoe UI',system-ui,sans-serif;}
@media(prefers-color-scheme:dark){:root{--bg:#141412;--fg:#e8e6e1;--muted:#888;--border:#2e2c28;--accent:#6baed6;--accent-light:#1a2a36;--locked:#d4a017;--locked-bg:#1e1800;--tag-bg:#252320;--tag-fg:#aaa;}}
*{box-sizing:border-box;margin:0;padding:0;}
body{background:var(--bg);color:var(--fg);font-family:var(--sans);font-size:16px;line-height:1.6;}
a{color:var(--accent);text-decoration:none;}
a:hover{text-decoration:underline;}
.container{max-width:var(--max);margin:0 auto;padding:0 1.25rem;}
.pieces{list-style:none;}
.piece-item{padding:1.25rem 0;border-bottom:1px solid var(--border);}
.piece-item:last-child{border-bottom:none;}
.piece-meta{font-size:.8rem;color:var(--muted);margin-bottom:.3rem;display:flex;gap:.75rem;align-items:center;flex-wrap:wrap;}
.type-badge{font-size:.7rem;text-transform:uppercase;letter-spacing:.05em;background:var(--tag-bg);color:var(--tag-fg);padding:1px 6px;border-radius:3px;}
.locked-badge{font-size:.7rem;background:var(--locked-bg);color:var(--locked);padding:1px 6px;border-radius:3px;border:1px solid var(--locked);}
.piece-title{font-size:1.1rem;font-weight:500;margin-bottom:.25rem;}
.piece-title a{color:var(--fg);}
.piece-title a:hover{color:var(--accent);text-decoration:none;}
.piece-desc{font-size:.9rem;color:var(--muted);}
.tags{display:flex;gap:.4rem;flex-wrap:wrap;margin-top:.4rem;}
.tag{font-size:.75rem;color:var(--muted);background:var(--tag-bg);padding:1px 7px;border-radius:10px;}
.empty{color:var(--muted);padding:2rem 0;text-align:center;}
.owner-bar{background:var(--accent-light);border:1px solid var(--accent);border-radius:6px;padding:.75rem 1rem;margin-bottom:1.5rem;font-size:.85rem;display:flex;justify-content:space-between;align-items:center;}
.btn{display:inline-block;padding:.4rem 1rem;border-radius:4px;font-size:.85rem;cursor:pointer;border:none;}
.btn-primary{background:var(--accent);color:#fff;}
.btn-primary:hover{opacity:.9;}
{{end}}

{{define "header"}}
<header style="border-bottom:1px solid var(--border);padding:1.5rem 0 1rem;margin-bottom:2rem;">
  <div style="font-size:1.1rem;font-weight:600;"><a href="/" style="color:var(--fg);">{{.Author}}</a></div>
  {{if .Bio}}<div style="font-size:.85rem;color:var(--muted);margin-top:.2rem;">{{.Bio}}</div>{{end}}
  <nav style="margin-top:.75rem;font-size:.85rem;color:var(--muted);display:flex;gap:1rem;align-items:center;">
    <span style="font-size:.75rem;background:var(--accent-light);color:var(--accent);padding:2px 8px;border-radius:4px;border:1px solid var(--accent);">humanMCP</span>
    {{if .IsOwner}}<a href="/logout" style="color:var(--muted);">logout</a>{{else}}<a href="/login" style="color:var(--muted);">owner</a>{{end}}
    <a href="/.well-known/mcp-server.json" style="color:var(--muted);" title="MCP discovery">⚙ mcp</a>
  </nav>
</header>
{{end}}

{{define "header-simple"}}
<header style="border-bottom:1px solid var(--border);padding:1rem 0 .75rem;margin-bottom:1.5rem;">
  <div style="font-size:1rem;font-weight:600;"><a href="/" style="color:var(--fg);">{{.Author}}</a></div>
</header>
{{end}}

{{define "footer"}}
<footer style="border-top:1px solid var(--border);margin-top:4rem;padding:1.5rem 0;font-size:.8rem;color:var(--muted);text-align:center;">
  <a href="/.well-known/mcp-server.json" style="color:var(--muted);">mcp endpoint</a> · humanMCP v0.1
</footer>
{{end}}

{{define "editor"}}
<div id="editor-overlay" style="display:none;position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.55);z-index:100;overflow-y:auto;padding:2rem;">
<div style="background:var(--bg);border-radius:8px;max-width:700px;margin:0 auto;padding:1.5rem;">
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:1rem;">
  <h2 style="font-size:1.1rem;" id="editor-title">New Piece</h2>
  <button onclick="closeEditor()" style="background:none;border:none;font-size:1.2rem;cursor:pointer;color:var(--muted);">✕</button>
</div>
<form id="editor-form" style="display:grid;gap:.65rem;">
  <div style="display:grid;grid-template-columns:1fr 1fr;gap:.5rem;">
    <input name="slug" placeholder="slug (e.g. the-fog)" style="padding:.5rem;border:1px solid var(--border);border-radius:4px;background:var(--bg);color:var(--fg);">
    <input name="title" placeholder="Title" style="padding:.5rem;border:1px solid var(--border);border-radius:4px;background:var(--bg);color:var(--fg);">
  </div>
  <div style="display:grid;grid-template-columns:1fr 1fr 1fr;gap:.5rem;">
    <select name="type" style="padding:.5rem;border:1px solid var(--border);border-radius:4px;background:var(--bg);color:var(--fg);">
      <option value="poem">poem</option><option value="essay">essay</option><option value="note">note</option><option value="audio">audio</option>
    </select>
    <select name="access" id="access-sel" onchange="updateGate()" style="padding:.5rem;border:1px solid var(--border);border-radius:4px;background:var(--bg);color:var(--fg);">
      <option value="public">public</option><option value="members">members</option><option value="locked">locked</option>
    </select>
    <select name="gate" id="gate-sel" style="padding:.5rem;border:1px solid var(--border);border-radius:4px;background:var(--bg);color:var(--fg);display:none;">
      <option value="challenge">challenge</option><option value="payment">payment (future)</option>
    </select>
  </div>
  <div id="challenge-fields" style="display:none;grid-template-columns:1fr 1fr;gap:.5rem;">
    <input name="challenge" placeholder="Challenge question" style="padding:.5rem;border:1px solid var(--border);border-radius:4px;background:var(--bg);color:var(--fg);">
    <input name="answer" placeholder="Answer" style="padding:.5rem;border:1px solid var(--border);border-radius:4px;background:var(--bg);color:var(--fg);">
  </div>
  <input name="description" placeholder="Short teaser / description (always public)" style="padding:.5rem;border:1px solid var(--border);border-radius:4px;background:var(--bg);color:var(--fg);">
  <input name="tags" placeholder="Tags: nature, grief, love" style="padding:.5rem;border:1px solid var(--border);border-radius:4px;background:var(--bg);color:var(--fg);">
  <textarea name="body" rows="14" placeholder="Write your poem or essay here..." style="padding:.5rem;border:1px solid var(--border);border-radius:4px;font-family:var(--serif);font-size:1rem;background:var(--bg);color:var(--fg);resize:vertical;line-height:1.8;"></textarea>
  <div style="display:flex;gap:.75rem;justify-content:flex-end;align-items:center;">
    <span id="editor-msg" style="font-size:.8rem;color:var(--muted);"></span>
    <button type="button" id="delete-btn" style="display:none;padding:.4rem .9rem;border-radius:4px;border:1px solid #c0392b;background:none;color:#c0392b;cursor:pointer;font-size:.85rem;" onclick="deletePiece()">Delete</button>
    <button type="button" onclick="closeEditor()" style="padding:.4rem 1rem;border:1px solid var(--border);border-radius:4px;background:var(--bg);color:var(--fg);cursor:pointer;font-size:.85rem;">Cancel</button>
    <button type="submit" class="btn btn-primary">Save</button>
  </div>
</form>
</div></div>
<script>
var currentSlug='';
var editToken=(document.cookie.split(';').find(function(c){return c.trim().startsWith('edit_token=');})||'').split('=').slice(1).join('=');
function openEditor(slug){
  document.getElementById('editor-overlay').style.display='block';
  document.body.style.overflow='hidden';
  currentSlug=slug||'';
  document.getElementById('editor-title').textContent=slug?'Edit: '+slug:'New Piece';
  document.getElementById('delete-btn').style.display=slug?'inline-block':'none';
  if(slug)loadPiece(slug);else{document.getElementById('editor-form').reset();updateGate();}
}
function closeEditor(){
  document.getElementById('editor-overlay').style.display='none';
  document.body.style.overflow='';
  document.getElementById('editor-form').reset();
  document.getElementById('editor-msg').textContent='';
}
function updateGate(){
  var a=document.getElementById('access-sel').value;
  document.getElementById('challenge-fields').style.display=(a==='locked')?'grid':'none';
  document.getElementById('gate-sel').style.display=(a==='locked')?'block':'none';
}
async function loadPiece(slug){
  var r=await fetch('/api/content/'+slug,{headers:{'X-Edit-Token':editToken}});
  if(!r.ok)return;
  var p=await r.json();
  var f=document.getElementById('editor-form');
  f.slug.value=p.Slug||'';f.title.value=p.Title||'';f.type.value=p.Type||'poem';
  f.access.value=p.Access||'public';f.gate.value=p.Gate||'challenge';
  f.challenge.value=p.Challenge||'';f.answer.value=p.Answer||'';
  f.description.value=p.Description||'';f.tags.value=(p.Tags||[]).join(', ');
  f.body.value=p.Body||'';updateGate();
}
async function deletePiece(){
  if(!currentSlug||!confirm('Delete "'+currentSlug+'"? This cannot be undone.'))return;
  var r=await fetch('/api/content/'+currentSlug,{method:'DELETE',headers:{'X-Edit-Token':editToken}});
  if((await r.json()).status==='deleted'){closeEditor();location.reload();}
}
document.getElementById('editor-form').addEventListener('submit',async function(e){
  e.preventDefault();
  var f=e.target,msg=document.getElementById('editor-msg');
  var slug=f.slug.value.trim();if(!slug){msg.textContent='Slug required.';return;}
  var body={slug:slug,title:f.title.value,type:f.type.value,access:f.access.value,gate:f.gate.value,
    challenge:f.challenge.value,answer:f.answer.value,description:f.description.value,
    tags:f.tags.value.split(',').map(function(t){return t.trim();}).filter(Boolean),body:f.body.value};
  var r=await fetch('/api/content/'+slug,{method:'PUT',headers:{'Content-Type':'application/json','X-Edit-Token':editToken},body:JSON.stringify(body)});
  var d=await r.json();
  if(d.error){msg.textContent='Error: '+d.error;return;}
  msg.textContent='Saved ✓';setTimeout(function(){closeEditor();location.reload();},600);
});
</script>
{{end}}
`
