/* ============================================================
   app.js — Draw & Guess frontend
   Phase 6: full-window canvas · multi-tool · colour picker
   ============================================================ */

(function () {
  'use strict';

  // ── DOM refs ─────────────────────────────────────────────
  const lobbyEl        = document.getElementById('lobby');
  const gameEl         = document.getElementById('game');
  const roomInput      = document.getElementById('room-input');
  const joinBtn        = document.getElementById('join-btn');
  const createBtn      = document.getElementById('create-btn');
  const leaveBtn       = document.getElementById('leave-btn');
  const confirmOverlay = document.getElementById('confirm-overlay');
  const confirmCancel  = document.getElementById('confirm-cancel');
  const confirmOk      = document.getElementById('confirm-ok');
  const statusMsg      = document.getElementById('status-msg');
  const statusMsgGame  = document.getElementById('status-msg-game');
  const roomLabel      = document.getElementById('room-label');
  const localCanvas    = document.getElementById('draw-canvas');
  const localCtx       = localCanvas.getContext('2d');
  const remoteCanvas   = document.getElementById('remote-canvas');
  const remoteCtx      = remoteCanvas.getContext('2d');
  const previewCanvas  = document.getElementById('preview-canvas');
  const previewCtx     = previewCanvas.getContext('2d');
  const lineWidthInput = document.getElementById('line-width');
  const clearBtn       = document.getElementById('clear-btn');

  // ── Client identity ───────────────────────────────────────
  const clientId = Math.random().toString(36).slice(2, 10);

  // ── Tool / colour state ───────────────────────────────────
  let currentTool  = 'pen';
  let currentColor = '#1a1a2e';
  let lineWidth    = 3;

  // ── Local drawing state ───────────────────────────────────
  /**
   * stroke — snapshot sent to server on every draw event:
   *   action    : 'start' | 'draw' | 'end'
   *   x, y      : current canvas position
   *   prevX/Y   : previous position (Bézier control point)
   *   tool, color, lineWidth : sender's current settings
   */
  let stroke = {
    action: 'idle', x: 0, y: 0, prevX: 0, prevY: 0,
    tool: 'pen', color: '#1a1a2e', lineWidth: 3,
  };
  let isPainting    = false;
  let shapeStart    = null;  // { x, y } for shape tools
  let currentRoomId = '';    // set when player_joined is received

  // ── Remote stroke state (per peer) ───────────────────────
  /**
   * remoteStrokes: Map<clientId → { active, lastMidX, lastMidY }>
   * Tracks Bézier continuation point per remote peer.
   */
  const remoteStrokes = new Map();

  // ── Canvas resize ─────────────────────────────────────────
  function resizeCanvases() {
    const W = window.innerWidth;
    const H = window.innerHeight;
    [localCanvas, remoteCanvas, previewCanvas].forEach(c => {
      c.width  = W;
      c.height = H;
    });
  }
  window.addEventListener('resize', resizeCanvases);

  // ── Coordinate helper ─────────────────────────────────────
  function getPos(canvas, clientX, clientY) {
    const rect   = canvas.getBoundingClientRect();
    const scaleX = canvas.width  / rect.width;
    const scaleY = canvas.height / rect.height;
    return {
      x: (clientX - rect.left) * scaleX,
      y: (clientY - rect.top)  * scaleY,
    };
  }

  // ── Apply pen/eraser style to a context ──────────────────
  function applyPenStyle(ctx, color, lw, isEraser) {
    ctx.strokeStyle = isEraser ? '#ffffff' : color;
    ctx.fillStyle   = isEraser ? '#ffffff' : color;
    ctx.lineWidth   = isEraser ? Math.max(lw * 5, 20) : lw;
    ctx.lineCap     = 'round';
    ctx.lineJoin    = 'round';
    ctx.globalCompositeOperation = 'source-over';
  }

  // ── Shape renderer (local + remote + preview) ─────────────
  function drawShape(ctx, tool, sx, sy, ex, ey, color, lw) {
    ctx.save();
    ctx.strokeStyle = color;
    ctx.lineWidth   = lw;
    ctx.lineCap     = 'round';
    ctx.lineJoin    = 'round';
    ctx.beginPath();

    switch (tool) {
      case 'rect':
        ctx.strokeRect(sx, sy, ex - sx, ey - sy);
        break;

      case 'triangle': {
        const mx = (sx + ex) / 2;
        ctx.moveTo(mx, sy);   // apex (top-centre)
        ctx.lineTo(ex, ey);   // bottom-right
        ctx.lineTo(sx, ey);   // bottom-left
        ctx.closePath();
        ctx.stroke();
        break;
      }

      case 'circle': {
        const rx = Math.abs(ex - sx) / 2;
        const ry = Math.abs(ey - sy) / 2;
        const cx = Math.min(sx, ex) + rx;
        const cy = Math.min(sy, ey) + ry;
        ctx.ellipse(cx, cy, rx || 1, ry || 1, 0, 0, Math.PI * 2);
        ctx.stroke();
        break;
      }
    }
    ctx.restore();
  }

  // ── Local freehand drawing ────────────────────────────────
  function startStroke(x, y) {
    isPainting = true;
    const isEraser = currentTool === 'eraser';
    stroke = {
      action: 'start', x, y, prevX: x, prevY: y,
      tool: currentTool, color: currentColor, lineWidth,
    };

    applyPenStyle(localCtx, currentColor, lineWidth, isEraser);
    localCtx.beginPath();
    localCtx.arc(x, y, localCtx.lineWidth / 2, 0, Math.PI * 2);
    localCtx.fill();
    localCtx.beginPath();
    localCtx.moveTo(x, y);

    sendDraw();
  }

  function continueStroke(x, y) {
    if (!isPainting) return;

    stroke.prevX  = stroke.x;
    stroke.prevY  = stroke.y;
    stroke.x      = x;
    stroke.y      = y;
    stroke.action = 'draw';

    const midX = (stroke.prevX + x) / 2;
    const midY = (stroke.prevY + y) / 2;
    localCtx.quadraticCurveTo(stroke.prevX, stroke.prevY, midX, midY);
    localCtx.stroke();
    localCtx.beginPath();
    localCtx.moveTo(midX, midY);

    sendDraw();
  }

  function endStroke() {
    if (!isPainting) return;
    isPainting    = false;
    stroke.action = 'end';
    localCtx.beginPath();
    sendDraw();
  }

  // ── Local shape drawing ───────────────────────────────────
  function startShape(x, y) {
    shapeStart = { x, y };
  }

  function previewShape(x, y) {
    if (!shapeStart) return;
    previewCtx.clearRect(0, 0, previewCanvas.width, previewCanvas.height);
    drawShape(previewCtx, currentTool,
      shapeStart.x, shapeStart.y, x, y, currentColor, lineWidth);
  }

  function finaliseShape(x, y) {
    if (!shapeStart) return;
    drawShape(localCtx, currentTool,
      shapeStart.x, shapeStart.y, x, y, currentColor, lineWidth);
    previewCtx.clearRect(0, 0, previewCanvas.width, previewCanvas.height);
    sendShape(shapeStart.x, shapeStart.y, x, y);
    shapeStart = null;
  }

  function cancelShape() {
    previewCtx.clearRect(0, 0, previewCanvas.width, previewCanvas.height);
    shapeStart = null;
  }

  // ── Unified pointer dispatch ──────────────────────────────
  function isFreeDraw() { return currentTool === 'pen' || currentTool === 'eraser'; }

  function onDown(x, y) {
    if (isFreeDraw()) startStroke(x, y);
    else              startShape(x, y);
  }

  function onMove(x, y) {
    if (isFreeDraw()) continueStroke(x, y);
    else              previewShape(x, y);
  }

  function onUp(x, y) {
    if (isFreeDraw()) endStroke();
    else              finaliseShape(x, y);
  }

  function onCancel() {
    if (isFreeDraw()) endStroke();
    else              cancelShape();
  }

  // ── Mouse events ──────────────────────────────────────────
  localCanvas.addEventListener('mousedown', (e) => {
    onDown(...Object.values(getPos(localCanvas, e.clientX, e.clientY)));
  });

  localCanvas.addEventListener('mousemove', (e) => {
    onMove(...Object.values(getPos(localCanvas, e.clientX, e.clientY)));
  });

  localCanvas.addEventListener('mouseup', (e) => {
    onUp(...Object.values(getPos(localCanvas, e.clientX, e.clientY)));
  });

  localCanvas.addEventListener('mouseout', onCancel);

  // ── Touch events ──────────────────────────────────────────
  localCanvas.addEventListener('touchstart', (e) => {
    e.preventDefault();
    const t = e.touches[0];
    onDown(...Object.values(getPos(localCanvas, t.clientX, t.clientY)));
  }, { passive: false });

  localCanvas.addEventListener('touchmove', (e) => {
    e.preventDefault();
    const t = e.touches[0];
    onMove(...Object.values(getPos(localCanvas, t.clientX, t.clientY)));
  }, { passive: false });

  localCanvas.addEventListener('touchend', (e) => {
    if (e.changedTouches.length) {
      const t = e.changedTouches[0];
      onUp(...Object.values(getPos(localCanvas, t.clientX, t.clientY)));
    } else { onCancel(); }
  });

  localCanvas.addEventListener('touchcancel', onCancel);

  // ── WebSocket ─────────────────────────────────────────────
  let ws = null;

  function buildWsUrl() {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    return `${proto}://${location.host}/ws`;
  }

  function connectWebSocket(roomId, action) {
    const url = buildWsUrl();
    console.log(`[WS] Connecting to ${url} …`);
    setStatus('連線中…');

    ws = new WebSocket(url);

    ws.addEventListener('open', () => {
      console.log('[WS] Connection established ✅');
      setStatus(action === 'create_room' ? '正在建立房間…' : '正在搜尋房間…');
      sendJSON({ event: action, room_id: roomId, data: {} });
    });

    ws.addEventListener('message', (evt) => {
      try { handleMessage(JSON.parse(evt.data)); }
      catch (e) { console.warn('[WS] Non-JSON:', evt.data); }
    });

    ws.addEventListener('close', (evt) => {
      console.log(`[WS] Closed (code ${evt.code})`);
      setGameStatus('連線已中斷');
    });

    ws.addEventListener('error', () => setGameStatus('連線錯誤'));
  }

  // ── Message router ────────────────────────────────────────
  function handleMessage(msg) {
    switch (msg.event) {
      case 'room_created':
        console.log(`[WS] room_created room="${msg.room_id}" ✅`);
        showGame(msg.room_id);
        break;

      case 'player_joined':
        console.log(`[WS] player_joined room="${msg.room_id}" ✅`);
        showGame(msg.room_id);
        break;

      case 'draw':
        drawRemote(msg);
        break;

      case 'clear':
        remoteCtx.clearRect(0, 0, remoteCanvas.width, remoteCanvas.height);
        break;

      case 'error':
        console.warn(`[WS] Error:`, msg.data.message);
        setStatus(msg.data.message || '發生錯誤');
        setLobbyBusy(false);
        if (ws) {
          ws.onclose = null; // Prevent showing '連線已中斷'
          ws.close();
          ws = null;
        }
        break;

      default:
        console.log('[WS] Unhandled:', msg.event);
    }
  }

  // ── Send helpers ──────────────────────────────────────────
  function sendDraw() {
    if (!currentRoomId) return;
    sendJSON({ event: 'draw', room_id: currentRoomId, client_id: clientId, data: stroke });
  }

  function sendShape(sx, sy, ex, ey) {
    if (!currentRoomId) return;
    sendJSON({
      event: 'draw', room_id: currentRoomId, client_id: clientId,
      data: {
        action: 'shape', tool: currentTool,
        color: currentColor, lineWidth,
        startX: sx, startY: sy, endX: ex, endY: ey,
      },
    });
  }

  function sendClear() {
    if (!currentRoomId) return;
    sendJSON({ event: 'clear', room_id: currentRoomId, client_id: clientId, data: {} });
  }

  // ── Remote rendering ──────────────────────────────────────
  /**
   * drawRemote — replays a remote peer's stroke/shape on remoteCtx.
   *
   * Freehand (pen/eraser):
   *   Uses per-peer lastMidX/Y to produce gapless Bézier curves,
   *   mirroring continueStroke() exactly.
   *
   * Shapes (rect/triangle/circle):
   *   A single 'shape' action carries all coordinates → drawn at once.
   */
  function drawRemote(msg) {
    const peerId = msg.client_id;
    if (!peerId) return;
    const d = msg.data;
    if (!d) return;

    const { action } = d;

    // ── Shape: one-shot draw ─────────────────────────────
    if (action === 'shape') {
      drawShape(remoteCtx, d.tool, d.startX, d.startY, d.endX, d.endY, d.color, d.lineWidth);
      return;
    }

    // ── Freehand / Eraser ────────────────────────────────
    const isEraser = d.tool === 'eraser';

    if (action === 'start') {
      remoteStrokes.set(peerId, {
        active: true, lastMidX: d.x, lastMidY: d.y,
      });

      applyPenStyle(remoteCtx, d.color, d.lineWidth, isEraser);
      remoteCtx.beginPath();
      remoteCtx.arc(d.x, d.y, remoteCtx.lineWidth / 2, 0, Math.PI * 2);
      remoteCtx.fill();
      remoteCtx.beginPath();
      remoteCtx.moveTo(d.x, d.y);

    } else if (action === 'draw') {
      const peer = remoteStrokes.get(peerId);
      if (!peer || !peer.active) return;

      const midX = (d.prevX + d.x) / 2;
      const midY = (d.prevY + d.y) / 2;

      applyPenStyle(remoteCtx, d.color, d.lineWidth, isEraser);
      remoteCtx.beginPath();
      remoteCtx.moveTo(peer.lastMidX, peer.lastMidY);
      remoteCtx.quadraticCurveTo(d.prevX, d.prevY, midX, midY);
      remoteCtx.stroke();
      remoteCtx.beginPath();
      remoteCtx.moveTo(midX, midY);

      peer.lastMidX = midX;
      peer.lastMidY = midY;

    } else if (action === 'end') {
      const peer = remoteStrokes.get(peerId);
      if (peer) { peer.active = false; remoteCtx.beginPath(); }
    }
  }

  // ── Generic helpers ───────────────────────────────────────
  function sendJSON(obj) {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(obj));
  }

  function setStatus(text)     { statusMsg.textContent     = text; }
  function setGameStatus(text) { statusMsgGame.textContent = text; }

  function showGame(roomId) {
    currentRoomId = roomId;
    resizeCanvases();         // size canvases to actual viewport
    lobbyEl.hidden = true;
    gameEl.hidden  = false;
    roomLabel.textContent = `房間：${roomId}`;
    setGameStatus('已連線');
  }

  // ── Toolbar: colour swatches ──────────────────────────────
  document.querySelectorAll('.color-swatch').forEach(btn => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.color-swatch').forEach(b => b.classList.remove('selected'));
      btn.classList.add('selected');
      currentColor = btn.dataset.color;
      // Auto-switch back to pen if eraser was active
      if (currentTool === 'eraser') selectTool('pen');
    });
  });

  // ── Toolbar: tool buttons ─────────────────────────────────
  function selectTool(tool) {
    currentTool = tool;
    document.querySelectorAll('.tool-btn').forEach(b =>
      b.classList.toggle('selected', b.dataset.tool === tool)
    );
    // Cursor feedback
    if (tool === 'eraser') {
      localCanvas.style.cursor = 'cell';
    } else if (tool === 'pen') {
      localCanvas.style.cursor = 'crosshair';
    } else {
      localCanvas.style.cursor = 'crosshair';
    }
  }

  document.querySelectorAll('.tool-btn').forEach(btn => {
    btn.addEventListener('click', () => selectTool(btn.dataset.tool));
  });

  // ── Toolbar: line width ───────────────────────────────────
  lineWidthInput.addEventListener('input', () => {
    lineWidth = parseInt(lineWidthInput.value, 10);
  });

  // ── Toolbar: clear button ─────────────────────────────────
  clearBtn.addEventListener('click', () => {
    localCtx.clearRect(0, 0, localCanvas.width, localCanvas.height);
    previewCtx.clearRect(0, 0, previewCanvas.width, previewCanvas.height);
    sendClear();
  });

  // ── Lobby UI ──────────────────────────────────────────────

  /** Disable / enable all lobby buttons together */
  function setLobbyBusy(busy) {
    joinBtn.disabled   = busy;
    createBtn.disabled = busy;
    roomInput.disabled = busy;
  }

  /** Join an existing room (user types the room ID) */
  joinBtn.addEventListener('click', () => {
    const roomId = roomInput.value.trim();
    if (!roomId) { setStatus('請輸入 6 位數房號'); return; }
    if (!/^\d{6}$/.test(roomId)) { setStatus('房號必須為 6 位數字'); return; }
    setLobbyBusy(true);
    connectWebSocket(roomId, 'join_room');
  });

  /** Create a new room with a random 6-digit ID */
  createBtn.addEventListener('click', () => {
    const roomId = String(Math.floor(Math.random() * 900000) + 100000);
    roomInput.value = roomId;
    setStatus(`建立房間中：${roomId}`);
    setLobbyBusy(true);
    connectWebSocket(roomId, 'create_room');
  });

  /** Enter key in input triggers join */
  roomInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') joinBtn.click();
  });

  // ── Leave Room ────────────────────────────────────────────

  /** Show confirmation modal */
  leaveBtn.addEventListener('click', () => {
    confirmOverlay.hidden = false;
  });

  /** Cancel — close modal */
  confirmCancel.addEventListener('click', () => {
    confirmOverlay.hidden = true;
  });

  /** Close modal on backdrop click */
  confirmOverlay.addEventListener('click', (e) => {
    if (e.target === confirmOverlay) confirmOverlay.hidden = true;
  });

  /** Confirm — leave room and return to lobby */
  confirmOk.addEventListener('click', () => {
    confirmOverlay.hidden = true;
    returnToLobby();
  });

  /**
   * returnToLobby — cleanly tears down the current session:
   *   1. Closes WebSocket connection
   *   2. Clears all canvas layers
   *   3. Resets all drawing / room state
   *   4. Resets toolbar to defaults
   *   5. Shows lobby, hides game
   */
  function returnToLobby() {
    // Close WebSocket gracefully
    if (ws) {
      ws.onclose = null; // prevent the "連線已中斷" status from firing
      ws.close();
      ws = null;
    }

    // Clear all canvas layers
    localCtx.clearRect(0, 0, localCanvas.width, localCanvas.height);
    remoteCtx.clearRect(0, 0, remoteCanvas.width, remoteCanvas.height);
    previewCtx.clearRect(0, 0, previewCanvas.width, previewCanvas.height);

    // Reset state
    currentRoomId = '';
    isPainting    = false;
    shapeStart    = null;
    remoteStrokes.clear();

    // Reset toolbar to defaults
    selectTool('pen');
    currentColor  = '#1a1a2e';
    lineWidth     = 3;
    lineWidthInput.value = 3;
    document.querySelectorAll('.color-swatch').forEach((b, i) => {
      b.classList.toggle('selected', i === 0);
    });

    // Reset lobby
    roomInput.value = '';
    setStatus('');
    setLobbyBusy(false);

    // Switch screens
    gameEl.hidden  = true;
    lobbyEl.hidden = false;
  }

  console.log('[app.js] loaded — clientId:', clientId);
})();
