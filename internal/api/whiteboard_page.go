package api

import (
	"io"
	"net/http"
)

func (s *Server) whiteboardPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'")
	io.WriteString(w, `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>KyDrive · Whiteboard</title>
  <meta name="viewport" content="width=device-width, initial-scale=1.0, user-scalable=no">
  <style>
    :root {
      --bg: #1c1c1e;
      --surface: #2c2c2e;
      --panel: #3a3a3c;
      --ink: #f2f2f7;
      --ink-muted: #8e8e93;
      --accent: #ff9f0a;
      --line: #48484a;
      --danger: #ff453a;
      --success: #32d74b;
    }
    * { box-sizing: border-box; user-select: none; }
    html, body { height: 100%; margin: 0; padding: 0; overflow: hidden; background: var(--bg); color: var(--ink); font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; }
    #app { display: flex; flex-direction: column; width: 100vw; height: 100vh; position: relative; }
    header { height: 50px; background: var(--surface); border-bottom: 1px solid var(--line); display: flex; align-items: center; justify-content: space-between; padding: 0 16px; z-index: 10; gap: 12px; }
    .header-left, .header-right { display: flex; align-items: center; gap: 10px; }
    .btn { background: var(--panel); border: 1px solid var(--line); color: var(--ink); border-radius: 6px; padding: 6px 12px; font-size: 13px; font-weight: 500; cursor: pointer; display: inline-flex; align-items: center; gap: 6px; text-decoration: none; transition: background 0.15s, border-color 0.15s; }
    .btn:hover { background: #48484a; }
    .btn:active { background: #545458; }
    .btn-primary { background: var(--accent); color: #000; font-weight: 600; border-color: var(--accent); }
    .btn-primary:hover { background: #e08b08; }
    .btn.active { background: var(--accent); color: #000; border-color: var(--accent); }
    #doc-title { font-weight: 600; font-size: 15px; max-width: 260px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    #save-status { font-size: 12px; color: var(--ink-muted); display: flex; align-items: center; gap: 4px; }
    #save-status.dirty { color: var(--accent); }
    #save-status.saved { color: var(--success); }
    #save-status.error { color: var(--danger); }
    #toolbar { position: absolute; top: 62px; left: 50%; transform: translateX(-50%); background: var(--surface); border: 1px solid var(--line); border-radius: 8px; padding: 4px 8px; display: flex; gap: 4px; z-index: 10; box-shadow: 0 4px 16px rgba(0,0,0,0.3); }
    .tool-btn { width: 36px; height: 36px; display: flex; align-items: center; justify-content: center; background: transparent; border: none; border-radius: 6px; color: var(--ink); font-size: 16px; cursor: pointer; }
    .tool-btn:hover { background: var(--panel); }
    .tool-btn.active { background: var(--accent); color: #000; }
    .sep { width: 1px; background: var(--line); margin: 4px 2px; }
    #props-panel { position: absolute; top: 114px; left: 16px; background: var(--surface); border: 1px solid var(--line); border-radius: 8px; padding: 10px 14px; z-index: 10; display: flex; flex-direction: column; gap: 8px; font-size: 12px; box-shadow: 0 4px 16px rgba(0,0,0,0.3); }
    .prop-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
    .color-swatches { display: flex; gap: 4px; }
    .swatch { width: 18px; height: 18px; border-radius: 50%; border: 1px solid #666; cursor: pointer; }
    .swatch.selected { outline: 2px solid var(--accent); outline-offset: 1px; }
    #canvas-container { flex: 1; position: relative; overflow: hidden; cursor: crosshair; background: #121214; }
    canvas { display: block; position: absolute; top: 0; left: 0; }
    #zoom-controls { position: absolute; bottom: 16px; left: 16px; background: var(--surface); border: 1px solid var(--line); border-radius: 6px; padding: 4px; display: flex; align-items: center; gap: 4px; z-index: 10; font-size: 12px; }
    #zoom-controls button { background: transparent; border: none; color: var(--ink); width: 26px; height: 26px; border-radius: 4px; cursor: pointer; font-size: 14px; }
    #zoom-controls button:hover { background: var(--panel); }
    #zoom-level { min-width: 44px; text-align: center; font-weight: 500; }
    #toast { position: absolute; bottom: 20px; left: 50%; transform: translateX(-50%); background: var(--panel); border: 1px solid var(--line); color: var(--ink); padding: 8px 16px; border-radius: 6px; font-size: 13px; z-index: 20; opacity: 0; pointer-events: none; transition: opacity 0.2s; }
    #toast.show { opacity: 1; }
  </style>
</head>
<body>
  <div id="app">
    <header>
      <div class="header-left">
        <a href="/" class="btn" title="Back to Drive">&larr; Drive</a>
        <span id="doc-title">Whiteboard</span>
        <span id="save-status">Saved</span>
      </div>
      <div class="header-right">
        <button id="btn-undo" class="btn" title="Undo (Ctrl+Z)">&#x21BA;</button>
        <button id="btn-redo" class="btn" title="Redo (Ctrl+Y)">&#x21BB;</button>
        <button id="btn-clear" class="btn" title="Clear Canvas">Clear</button>
        <button id="btn-export-png" class="btn" title="Export as PNG">Export PNG</button>
        <button id="btn-save" class="btn btn-primary" title="Save document (Ctrl+S)">Save</button>
      </div>
    </header>

    <div id="toolbar">
      <button class="tool-btn active" data-tool="select" title="Selection (V)">&#x261E;</button>
      <button class="tool-btn" data-tool="freedraw" title="Pencil / Draw (P)">&#x270E;</button>
      <div class="sep"></div>
      <button class="tool-btn" data-tool="rectangle" title="Rectangle (R)">&#x25AD;</button>
      <button class="tool-btn" data-tool="diamond" title="Diamond (D)">&#x25C7;</button>
      <button class="tool-btn" data-tool="ellipse" title="Circle / Ellipse (O)">&#x25EF;</button>
      <button class="tool-btn" data-tool="arrow" title="Arrow (A)">&#x2794;</button>
      <button class="tool-btn" data-tool="line" title="Line (L)">&#x2500;</button>
      <button class="tool-btn" data-tool="text" title="Text Note (T)">T</button>
      <div class="sep"></div>
      <button class="tool-btn" data-tool="eraser" title="Eraser (E)">&#x232B;</button>
    </div>

    <div id="props-panel">
      <div class="prop-row">
        <span>Stroke</span>
        <div class="color-swatches" id="stroke-swatches">
          <div class="swatch selected" data-color="#ffffff" style="background:#ffffff"></div>
          <div class="swatch" data-color="#ff9f0a" style="background:#ff9f0a"></div>
          <div class="swatch" data-color="#32d74b" style="background:#32d74b"></div>
          <div class="swatch" data-color="#0a84ff" style="background:#0a84ff"></div>
          <div class="swatch" data-color="#ff453a" style="background:#ff453a"></div>
        </div>
      </div>
      <div class="prop-row">
        <span>Background</span>
        <div class="color-swatches" id="bg-swatches">
          <div class="swatch selected" data-color="transparent" style="background:transparent;border:1px dashed #888"></div>
          <div class="swatch" data-color="rgba(255,159,10,0.25)" style="background:#ff9f0a"></div>
          <div class="swatch" data-color="rgba(50,215,75,0.25)" style="background:#32d74b"></div>
          <div class="swatch" data-color="rgba(10,132,255,0.25)" style="background:#0a84ff"></div>
          <div class="swatch" data-color="rgba(255,69,58,0.25)" style="background:#ff453a"></div>
        </div>
      </div>
      <div class="prop-row">
        <span>Width</span>
        <div style="display:flex;gap:4px">
          <button class="btn stroke-width-btn active" data-width="2">Thin</button>
          <button class="btn stroke-width-btn" data-width="4">Med</button>
          <button class="btn stroke-width-btn" data-width="8">Bold</button>
        </div>
      </div>
    </div>

    <div id="canvas-container">
      <canvas id="canvas"></canvas>
    </div>

    <div id="zoom-controls">
      <button id="btn-zoom-out" title="Zoom Out">&minus;</button>
      <span id="zoom-level">100%</span>
      <button id="btn-zoom-in" title="Zoom In">&plus;</button>
      <button id="btn-zoom-reset" title="Reset Zoom">1:1</button>
    </div>

    <div id="toast"></div>
  </div>
  <script src="/whiteboard-bootstrap.js"></script>
</body>
</html>`)
}

func (s *Server) whiteboardScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, `(async () => {
  const container = document.getElementById('canvas-container');
  const canvas = document.getElementById('canvas');
  const ctx = canvas.getContext('2d');
  const titleEl = document.getElementById('doc-title');
  const statusEl = document.getElementById('save-status');
  const zoomLevelEl = document.getElementById('zoom-level');
  const toastEl = document.getElementById('toast');

  let fileMeta = null;
  let fileId = new URLSearchParams(location.search).get('file');
  let elements = [];
  let history = [];
  let historyIdx = -1;
  let activeTool = 'select';
  let strokeColor = '#ffffff';
  let fillColor = 'transparent';
  let strokeWidth = 2;
  let isDrawing = false;
  let currentElement = null;
  let dirty = false;
  let saving = false;
  let scale = 1;
  let panX = 0;
  let panY = 0;
  let isPanning = false;
  let lastPanPoint = { x: 0, y: 0 };
  let saveTimer = null;

  function showToast(msg) {
    toastEl.textContent = msg;
    toastEl.classList.add('show');
    setTimeout(() => toastEl.classList.remove('show'), 2500);
  }

  function getCsrf() {
    const c = document.cookie.split('; ').find(x => x.startsWith('ky_csrf='));
    return c ? decodeURIComponent(c.slice(8)) : '';
  }

  function resize() {
    const dpr = window.devicePixelRatio || 1;
    const w = container.clientWidth;
    const h = container.clientHeight;
    canvas.width = w * dpr;
    canvas.height = h * dpr;
    canvas.style.width = w + 'px';
    canvas.style.height = h + 'px';
    render();
  }
  window.addEventListener('resize', resize);

  function pushHistory() {
    if (historyIdx < history.length - 1) {
      history = history.slice(0, historyIdx + 1);
    }
    history.push(JSON.stringify(elements));
    if (history.length > 50) history.shift();
    historyIdx = history.length - 1;
    markDirty();
  }

  function markDirty() {
    dirty = true;
    statusEl.textContent = 'Unsaved changes';
    statusEl.className = 'dirty';
    clearTimeout(saveTimer);
    saveTimer = setTimeout(save, 3000);
  }

  async function save() {
    if (!fileMeta || saving || !dirty) return;
    clearTimeout(saveTimer);
    saving = true;
    statusEl.textContent = 'Saving…';
    statusEl.className = 'dirty';

    const scene = {
      type: "excalidraw",
      version: 2,
      source: "https://kydrive.busnes.app",
      elements: elements,
      appState: {
        gridSize: null,
        viewBackgroundColor: "#121214"
      },
      files: {}
    };

    const payload = JSON.stringify(scene, null, 2);
    const q = new URLSearchParams({
      name: fileMeta.name,
      parent: fileMeta.parent || '',
      revision: String(fileMeta.revision),
      file: fileMeta.id
    });

    try {
      const resp = await fetch('/api/drive/workspaces/' + encodeURIComponent(fileMeta.workspace) + '/uploads?' + q, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/octet-stream',
          'X-CSRF-Token': getCsrf()
        },
        body: payload
      });
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error || 'Save failed');
      fileMeta.revision = data.revision;
      dirty = false;
      statusEl.textContent = 'Saved';
      statusEl.className = 'saved';
    } catch (err) {
      statusEl.textContent = 'Save failed: ' + (err.message || 'error');
      statusEl.className = 'error';
    } finally {
      saving = false;
    }
  }

  function screenToWorld(sx, sy) {
    const dpr = window.devicePixelRatio || 1;
    return {
      x: (sx - panX) / scale,
      y: (sy - panY) / scale
    };
  }

  function render() {
    const dpr = window.devicePixelRatio || 1;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, canvas.width, canvas.height);

    ctx.save();
    ctx.translate(panX, panY);
    ctx.scale(scale, scale);

    // Draw grid
    const gridSize = 30;
    const startX = Math.floor(-panX / scale / gridSize) * gridSize - gridSize;
    const startY = Math.floor(-panY / scale / gridSize) * gridSize - gridSize;
    const endX = startX + (container.clientWidth / scale) + gridSize * 2;
    const endY = startY + (container.clientHeight / scale) + gridSize * 2;

    ctx.strokeStyle = '#232326';
    ctx.lineWidth = 1 / scale;
    ctx.beginPath();
    for (let x = startX; x <= endX; x += gridSize) {
      ctx.moveTo(x, startY);
      ctx.lineTo(x, endY);
    }
    for (let y = startY; y <= endY; y += gridSize) {
      ctx.moveTo(startX, y);
      ctx.lineTo(endX, y);
    }
    ctx.stroke();

    // Render elements
    for (const el of elements) {
      drawElement(el);
    }
    if (currentElement) {
      drawElement(currentElement);
    }

    ctx.restore();
    zoomLevelEl.textContent = Math.round(scale * 100) + '%';
  }

  function drawElement(el) {
    ctx.save();
    ctx.strokeStyle = el.strokeColor || '#ffffff';
    ctx.fillStyle = el.backgroundColor || 'transparent';
    ctx.lineWidth = el.strokeWidth || 2;
    ctx.lineCap = 'round';
    ctx.lineJoin = 'round';

    switch (el.type) {
      case 'rectangle': {
        const x = Math.min(el.x, el.x + el.width);
        const y = Math.min(el.y, el.y + el.height);
        const w = Math.abs(el.width);
        const h = Math.abs(el.height);
        if (el.backgroundColor && el.backgroundColor !== 'transparent') {
          ctx.fillRect(x, y, w, h);
        }
        ctx.strokeRect(x, y, w, h);
        break;
      }
      case 'diamond': {
        const cx = el.x + el.width / 2;
        const cy = el.y + el.height / 2;
        ctx.beginPath();
        ctx.moveTo(cx, el.y);
        ctx.lineTo(el.x + el.width, cy);
        ctx.lineTo(cx, el.y + el.height);
        ctx.lineTo(el.x, cy);
        ctx.closePath();
        if (el.backgroundColor && el.backgroundColor !== 'transparent') ctx.fill();
        ctx.stroke();
        break;
      }
      case 'ellipse': {
        const rx = Math.abs(el.width / 2);
        const ry = Math.abs(el.height / 2);
        const cx = el.x + el.width / 2;
        const cy = el.y + el.height / 2;
        ctx.beginPath();
        ctx.ellipse(cx, cy, rx, ry, 0, 0, Math.PI * 2);
        if (el.backgroundColor && el.backgroundColor !== 'transparent') ctx.fill();
        ctx.stroke();
        break;
      }
      case 'line':
      case 'arrow': {
        ctx.beginPath();
        ctx.moveTo(el.x, el.y);
        ctx.lineTo(el.x + el.width, el.y + el.height);
        ctx.stroke();
        if (el.type === 'arrow') {
          const angle = Math.atan2(el.height, el.width);
          const headlen = 14;
          const endX = el.x + el.width;
          const endY = el.y + el.height;
          ctx.beginPath();
          ctx.moveTo(endX, endY);
          ctx.lineTo(endX - headlen * Math.cos(angle - Math.PI / 6), endY - headlen * Math.sin(angle - Math.PI / 6));
          ctx.moveTo(endX, endY);
          ctx.lineTo(endX - headlen * Math.cos(angle + Math.PI / 6), endY - headlen * Math.sin(angle + Math.PI / 6));
          ctx.stroke();
        }
        break;
      }
      case 'freedraw': {
        if (!el.points || el.points.length < 2) break;
        ctx.beginPath();
        ctx.moveTo(el.x + el.points[0][0], el.y + el.points[0][1]);
        for (let i = 1; i < el.points.length; i++) {
          ctx.lineTo(el.x + el.points[i][0], el.y + el.points[i][1]);
        }
        ctx.stroke();
        break;
      }
      case 'text': {
        ctx.font = (el.fontSize || 18) + 'px -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif';
        ctx.fillStyle = el.strokeColor || '#ffffff';
        const lines = (el.text || '').split('\n');
        for (let i = 0; i < lines.length; i++) {
          ctx.fillText(lines[i], el.x, el.y + (i + 1) * (el.fontSize || 18));
        }
        break;
      }
    }
    ctx.restore();
  }

  // Pointer events
  let pointerStart = { x: 0, y: 0 };
  container.addEventListener('pointerdown', e => {
    if (e.button === 1 || (e.button === 0 && e.spaceKey) || activeTool === 'pan') {
      isPanning = true;
      lastPanPoint = { x: e.clientX, y: e.clientY };
      return;
    }
    if (e.button !== 0) return;

    const pt = screenToWorld(e.clientX, e.clientY - 50);
    pointerStart = pt;
    isDrawing = true;

    if (activeTool === 'eraser') {
      eraseAt(pt);
      return;
    }

    if (activeTool === 'text') {
      const text = prompt('Enter note text:');
      if (text) {
        elements.push({
          id: 't_' + Date.now(),
          type: 'text',
          x: pt.x,
          y: pt.y,
          text: text,
          fontSize: 20,
          strokeColor: strokeColor,
          backgroundColor: 'transparent'
        });
        pushHistory();
        render();
      }
      isDrawing = false;
      return;
    }

    if (activeTool === 'freedraw') {
      currentElement = {
        id: 'f_' + Date.now(),
        type: 'freedraw',
        x: pt.x,
        y: pt.y,
        strokeColor: strokeColor,
        strokeWidth: strokeWidth,
        points: [[0, 0]]
      };
    } else if (['rectangle', 'diamond', 'ellipse', 'line', 'arrow'].includes(activeTool)) {
      currentElement = {
        id: 's_' + Date.now(),
        type: activeTool,
        x: pt.x,
        y: pt.y,
        width: 0,
        height: 0,
        strokeColor: strokeColor,
        backgroundColor: fillColor,
        strokeWidth: strokeWidth
      };
    }
    render();
  });

  window.addEventListener('pointermove', e => {
    if (isPanning) {
      panX += e.clientX - lastPanPoint.x;
      panY += e.clientY - lastPanPoint.y;
      lastPanPoint = { x: e.clientX, y: e.clientY };
      render();
      return;
    }
    if (!isDrawing) return;

    const pt = screenToWorld(e.clientX, e.clientY - 50);
    if (activeTool === 'eraser') {
      eraseAt(pt);
      return;
    }

    if (currentElement) {
      if (currentElement.type === 'freedraw') {
        currentElement.points.push([pt.x - currentElement.x, pt.y - currentElement.y]);
      } else {
        currentElement.width = pt.x - currentElement.x;
        currentElement.height = pt.y - currentElement.y;
      }
      render();
    }
  });

  window.addEventListener('pointerup', () => {
    if (isPanning) {
      isPanning = false;
      return;
    }
    if (isDrawing && currentElement) {
      elements.push(currentElement);
      currentElement = null;
      pushHistory();
      render();
    }
    isDrawing = false;
  });

  function eraseAt(pt) {
    const threshold = 15 / scale;
    const initialLen = elements.length;
    elements = elements.filter(el => {
      const dx = pt.x - el.x;
      const dy = pt.y - el.y;
      if (el.width !== undefined && el.height !== undefined) {
        if (pt.x >= Math.min(el.x, el.x + el.width) - threshold &&
            pt.x <= Math.max(el.x, el.x + el.width) + threshold &&
            pt.y >= Math.min(el.y, el.y + el.height) - threshold &&
            pt.y <= Math.max(el.y, el.y + el.height) + threshold) {
          return false;
        }
      } else if (Math.hypot(dx, dy) < threshold) {
        return false;
      }
      return true;
    });
    if (elements.length !== initialLen) {
      pushHistory();
      render();
    }
  }

  // Wheel zoom
  container.addEventListener('wheel', e => {
    e.preventDefault();
    const zoomFactor = e.deltaY < 0 ? 1.1 : 0.9;
    const newScale = Math.min(Math.max(scale * zoomFactor, 0.2), 5);
    const mouseX = e.clientX;
    const mouseY = e.clientY - 50;

    panX = mouseX - (mouseX - panX) * (newScale / scale);
    panY = mouseY - (mouseY - panY) * (newScale / scale);
    scale = newScale;
    render();
  }, { passive: false });

  // Tool buttons
  document.querySelectorAll('.tool-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.tool-btn').forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      activeTool = btn.dataset.tool;
    });
  });

  // Color & width buttons
  document.querySelectorAll('#stroke-swatches .swatch').forEach(s => {
    s.addEventListener('click', () => {
      document.querySelectorAll('#stroke-swatches .swatch').forEach(x => x.classList.remove('selected'));
      s.classList.add('selected');
      strokeColor = s.dataset.color;
    });
  });
  document.querySelectorAll('#bg-swatches .swatch').forEach(s => {
    s.addEventListener('click', () => {
      document.querySelectorAll('#bg-swatches .swatch').forEach(x => x.classList.remove('selected'));
      s.classList.add('selected');
      fillColor = s.dataset.color;
    });
  });
  document.querySelectorAll('.stroke-width-btn').forEach(b => {
    b.addEventListener('click', () => {
      document.querySelectorAll('.stroke-width-btn').forEach(x => x.classList.remove('active'));
      b.classList.add('active');
      strokeWidth = parseInt(b.dataset.width, 10);
    });
  });

  // Undo / Redo
  document.getElementById('btn-undo').addEventListener('click', () => {
    if (historyIdx > 0) {
      historyIdx--;
      elements = JSON.parse(history[historyIdx]);
      markDirty();
      render();
    }
  });
  document.getElementById('btn-redo').addEventListener('click', () => {
    if (historyIdx < history.length - 1) {
      historyIdx++;
      elements = JSON.parse(history[historyIdx]);
      markDirty();
      render();
    }
  });

  document.getElementById('btn-clear').addEventListener('click', () => {
    if (confirm('Clear the entire whiteboard?')) {
      elements = [];
      pushHistory();
      render();
    }
  });

  document.getElementById('btn-save').addEventListener('click', save);

  document.getElementById('btn-export-png').addEventListener('click', () => {
    const link = document.createElement('a');
    link.download = (fileMeta?.name.replace(/\.[^/.]+$/, "") || 'whiteboard') + '.png';
    link.href = canvas.toDataURL('image/png');
    link.click();
  });

  // Zoom controls
  document.getElementById('btn-zoom-in').addEventListener('click', () => { scale = Math.min(scale * 1.2, 5); render(); });
  document.getElementById('btn-zoom-out').addEventListener('click', () => { scale = Math.max(scale / 1.2, 0.2); render(); });
  document.getElementById('btn-zoom-reset').addEventListener('click', () => { scale = 1; panX = 0; panY = 0; render(); });

  // Keyboard shortcuts
  window.addEventListener('keydown', e => {
    if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA') return;
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
      e.preventDefault();
      save();
    } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'z') {
      e.preventDefault();
      if (e.shiftKey) {
        if (historyIdx < history.length - 1) { historyIdx++; elements = JSON.parse(history[historyIdx]); markDirty(); render(); }
      } else {
        if (historyIdx > 0) { historyIdx--; elements = JSON.parse(history[historyIdx]); markDirty(); render(); }
      }
    } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'y') {
      e.preventDefault();
      if (historyIdx < history.length - 1) { historyIdx++; elements = JSON.parse(history[historyIdx]); markDirty(); render(); }
    }
  });

  // Initialize
  try {
    if (!fileId) throw new Error('No document specified in URL');
    const infoResp = await fetch('/api/drive/files/' + encodeURIComponent(fileId));
    if (!infoResp.ok) throw new Error('File not found or access denied');
    fileMeta = await infoResp.json();
    titleEl.textContent = fileMeta.name;
    document.title = fileMeta.name + ' · KyDrive Whiteboard';

    const dlResp = await fetch('/api/drive/files/' + encodeURIComponent(fileId) + '/download');
    if (dlResp.ok) {
      try {
        const scene = await dlResp.json();
        if (scene && Array.isArray(scene.elements)) {
          elements = scene.elements;
        }
      } catch {
        // Blank or non-json starting state
      }
    }
    history = [JSON.stringify(elements)];
    historyIdx = 0;
    resize();
  } catch (err) {
    statusEl.textContent = err.message;
    statusEl.className = 'error';
    showToast(err.message);
  }
})();
`)
}
