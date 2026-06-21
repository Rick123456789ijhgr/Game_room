/* ============================================================
   app.js — Draw & Guess frontend
   Phase 6: full-window canvas · multi-tool · colour picker
   ============================================================ */

(function () {
  'use strict';

  // ── DOM refs ─────────────────────────────────────────────
  const lobbyEl = document.getElementById('lobby');
  const waitingRoomEl = document.getElementById('waiting-room');
  const gameEl = document.getElementById('game');
  const roomInput = document.getElementById('room-input');
  const nickInput = document.getElementById('nick-input');
  const badgeAvatar = document.getElementById('badge-avatar');
  const joinBtn = document.getElementById('join-btn');
  const createBtn = document.getElementById('create-btn');
  const leaveBtn = document.getElementById('leave-btn');
  const confirmOverlay = document.getElementById('confirm-overlay');
  const confirmCancel = document.getElementById('confirm-cancel');
  const confirmOk = document.getElementById('confirm-ok');
  const statusMsg = document.getElementById('status-msg');
  const statusMsgGame = document.getElementById('status-msg-game');
  const roomLabel = document.getElementById('room-label');
  const memberCount = document.getElementById('member-count');
  const memberList = document.getElementById('member-list');
  const memberToggle = document.getElementById('member-toggle');
  const memberSection = document.getElementById('member-section');
  const sidebarDivider = document.querySelector('.sidebar-divider');
  const roomClosedOverlay = document.getElementById('room-closed-overlay');
  const roomClosedOk = document.getElementById('room-closed-ok');
  const chatPanel = document.getElementById('chat-panel');
  const chatMessages = document.getElementById('chat-messages');
  const chatInput = document.getElementById('chat-input');
  const chatSend = document.getElementById('chat-send');
  const localCanvas = document.getElementById('draw-canvas');
  const localCtx = localCanvas.getContext('2d');
  const remoteCanvas = document.getElementById('remote-canvas');
  const remoteCtx = remoteCanvas.getContext('2d');
  const previewCanvas = document.getElementById('preview-canvas');
  const previewCtx = previewCanvas.getContext('2d');
  const lineWidthInputs = [document.getElementById('line-width'), document.getElementById('line-width-left')];
  const clearBtns = [document.getElementById('clear-btn'), document.getElementById('clear-btn-left')];
  // Waiting room refs
  const wrRoomLabel = document.getElementById('wr-room-label');
  const wrMemberList = document.getElementById('wr-member-list');
  const wrReadyBtn = document.getElementById('wr-ready-btn');
  const wrStartBtn = document.getElementById('wr-start-btn');
  const wrStatus = document.getElementById('wr-status');
  const wrLeaveBtn = document.getElementById('wr-leave-btn');
  const wrScoreMinus = document.getElementById('wr-score-minus');
  const wrScorePlus = document.getElementById('wr-score-plus');
  const wrTargetScoreDisplay = document.getElementById('wr-target-score-display');
  const wrScoreGoal = document.getElementById('wr-score-goal');
  const wrSettingsWrap = document.getElementById('wr-settings-wrap');

  // ── Nickname state ────────────────────────────────────────────
  let myNickname = '匿名玩家';

  // Live update badge avatar as user types
  nickInput.addEventListener('input', () => {
    const val = nickInput.value.trim();
    badgeAvatar.textContent = val ? val[0].toUpperCase() : '?';
  });

  // ── Sidebar toggling ──────────────────────────────────────
  // Default: member section is collapsed (no expanded class)
  memberToggle.style.transform = 'rotate(-90deg)'; // arrow points right when collapsed

  document.getElementById('member-panel-header').addEventListener('click', () => {
    const isExpanded = memberSection.classList.contains('expanded');

    // Hide scrollbar during animation
    memberList.style.overflowY = 'hidden';

    if (isExpanded) {
      memberSection.classList.remove('expanded');
      memberToggle.style.transform = 'rotate(-90deg)';
    } else {
      memberSection.classList.add('expanded');
      memberToggle.style.transform = 'rotate(0deg)';
    }

    // Restore scrollbar only after expansion finishes
    setTimeout(() => {
      if (memberSection.classList.contains('expanded')) {
        memberList.style.overflowY = 'auto';
      }
    }, 300); // matches the 0.3s CSS transition duration
  });

  // ── Client identity ───────────────────────────────────────
  const clientId = Math.random().toString(36).slice(2, 10);

  // ── Tool / colour state ───────────────────────────────────
  let currentTool = 'pen';
  let currentColor = '#1a1a2e';
  let lineWidth = 3;

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
  let isPainting = false;
  let shapeStart = null;  // { x, y } for shape tools
  let currentRoomId = '';    // set when player_joined is received
  let amIHost = false;       // track local player's host status
  let myRole = 'guesser';    // 'drawer' or 'guesser'

  // ── Round timer state ─────────────────────────────────
  let roundTimerInterval = null;

  // ── Score state ──────────────────────────────────────
  let targetScore = 10;       // win condition
  let currentScores = {};     // nickname → score
  let wrTargetScore = 10;     // setting in waiting room

  // ── Remote stroke state (per peer) ───────────────────────
  /**
   * remoteStrokes: Map<clientId → { active, lastMidX, lastMidY }>
   * Tracks Bézier continuation point per remote peer.
   */
  const remoteStrokes = new Map();

  // ── Canvas resize (preserves drawn content) ───────────────
  function resizeCanvases() {
    const W = window.innerWidth;
    const H = window.innerHeight;

    // Preserve content from draw and remote canvases before resizing
    const localImg = localCtx.getImageData(0, 0, localCanvas.width, localCanvas.height);
    const remoteImg = remoteCtx.getImageData(0, 0, remoteCanvas.width, remoteCanvas.height);

    [localCanvas, remoteCanvas, previewCanvas].forEach(c => {
      c.width = W;
      c.height = H;
    });

    // Restore preserved content
    localCtx.putImageData(localImg, 0, 0);
    remoteCtx.putImageData(remoteImg, 0, 0);
  }

  // Initial size on first load (no content yet, safe to just resize)
  function resizeCanvasesFirst() {
    const W = window.innerWidth;
    const H = window.innerHeight;
    [localCanvas, remoteCanvas, previewCanvas].forEach(c => {
      c.width = W;
      c.height = H;
    });
  }

  window.addEventListener('resize', resizeCanvases);
  resizeCanvasesFirst();

  // ── Coordinate helper ─────────────────────────────────────
  function getPos(canvas, clientX, clientY) {
    const rect = canvas.getBoundingClientRect();
    const scaleX = canvas.width / rect.width;
    const scaleY = canvas.height / rect.height;
    return {
      x: (clientX - rect.left) * scaleX,
      y: (clientY - rect.top) * scaleY,
    };
  }

  // ── Apply pen/eraser style to a context ──────────────────
  function applyPenStyle(ctx, color, lw, isEraser) {
    ctx.strokeStyle = isEraser ? '#ffffff' : color;
    ctx.fillStyle = isEraser ? '#ffffff' : color;
    ctx.lineWidth = isEraser ? Math.max(lw * 5, 20) : lw;
    ctx.lineCap = 'round';
    ctx.lineJoin = 'round';
    ctx.globalCompositeOperation = 'source-over';
  }

  // ── Shape renderer (local + remote + preview) ─────────────
  function drawShape(ctx, tool, sx, sy, ex, ey, color, lw) {
    ctx.save();
    ctx.strokeStyle = color;
    ctx.lineWidth = lw;
    ctx.lineCap = 'round';
    ctx.lineJoin = 'round';
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

    stroke.prevX = stroke.x;
    stroke.prevY = stroke.y;
    stroke.x = x;
    stroke.y = y;
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
    isPainting = false;
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
    if (myRole !== 'drawer') return;
    if (isFreeDraw()) startStroke(x, y);
    else startShape(x, y);
  }

  function onMove(x, y) {
    if (myRole !== 'drawer') return;
    if (isFreeDraw()) continueStroke(x, y);
    else previewShape(x, y);
  }

  function onUp(x, y) {
    if (myRole !== 'drawer') return;
    if (isFreeDraw()) endStroke();
    else finaliseShape(x, y);
  }

  function onCancel() {
    if (myRole !== 'drawer') return;
    if (isFreeDraw()) endStroke();
    else cancelShape();
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
      sendJSON({ event: action, room_id: roomId, client_id: clientId, data: { nickname: myNickname } });
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
        amIHost = true;
        showWaitingRoom(msg.room_id);
        break;

      case 'player_joined': {
        const nick = msg.data && msg.data.nickname ? msg.data.nickname : '匿名玩家';
        console.log(`[WS] player_joined room="${msg.room_id}" nickname="${nick}" ✅`);
        // If it's me joining (non-host), go to waiting room
        if (waitingRoomEl.hidden && gameEl.hidden && nick === myNickname) {
          amIHost = false;
          showWaitingRoom(msg.room_id);
        } else if (!gameEl.hidden) {
          // Already in game canvas, show notification
          setGameStatus(`${nick} 加入了房間`);
          appendSystemMsg(`🟡 ${nick} 加入了房間`);
        }
        break;
      }

      case 'member_list':
        if (Array.isArray(msg.data)) {
          renderWaitingRoomMembers(msg.data);
          // Also update sidebar member list if in game
          renderMembers(msg.data);
        }
        break;

      case 'room_closed':
        console.log(`[WS] room_closed for room "${msg.room_id}"`);
        if (ws) {
          ws.onclose = null;
          ws.close();
          ws = null;
        }
        roomClosedOverlay.hidden = false;
        break;

      case 'kicked':
        console.log('[WS] kicked from room');
        if (ws) { ws.onclose = null; ws.close(); ws = null; }
        alert('你被房主踢出了房間。');
        returnToLobby();
        break;

      case 'game_start': {
        console.log('[WS] game_start — entering canvas');
        const role = msg.data && msg.data.role ? msg.data.role : 'guesser';
        const topic = msg.data && msg.data.topic ? msg.data.topic : '';
        const drawerNick = msg.data && msg.data.drawer_nick ? msg.data.drawer_nick : '';
        if (msg.data && msg.data.scores) currentScores = msg.data.scores;
        if (msg.data && msg.data.target_score) targetScore = msg.data.target_score;
        const isOvertime = msg.data && msg.data.overtime;
        const isFirstRound = gameEl.hidden; // still hidden → first time entering game
        stopRoundTimer();
        showGame(currentRoomId, role, topic, drawerNick, isOvertime);
        renderScoreboard();
        if (isFirstRound) {
          appendSystemMsg(isOvertime ? '⏰ 加時賽開始！' : '🎮 遊戲開始！');
        } else {
          const drawerLabel = drawerNick ? `「${drawerNick}」` : '新畫家';
          appendSystemMsg(`🔄 換題了！${drawerLabel} 擔任畫家`);
        }
        break;
      }

      case 'guess_result':
        const isCorrect = msg.data.correct;
        const guessStr = msg.data.guess;
        const nick = msg.data.nickname;

        const historyList = document.getElementById('guess-history-list');
        const item = document.createElement('div');
        item.className = 'guess-item ' + (isCorrect ? 'correct' : 'wrong');
        item.textContent = (isCorrect ? '✅ ' : '❌ ') + nick + ': ' + guessStr;
        historyList.appendChild(item);
        historyList.scrollTop = historyList.scrollHeight;

        if (isCorrect && myRole === 'guesser') {
          const ansInput = document.getElementById('answer-input');
          const ansSend = document.getElementById('answer-send');

          ansInput.disabled = true;
          ansInput.classList.add('locked');
          ansInput.value = '🔒 你已答對！請等待回合結束';

          ansSend.disabled = true;
          ansSend.classList.add('locked');

          const correctOverlay = document.getElementById('correct-overlay');
          if (correctOverlay) {
            correctOverlay.hidden = false;
            setTimeout(() => correctOverlay.hidden = true, 2500);
          }
        }
        break;

      case 'chat': {
        const d = msg.data || {};
        const isSelf = d.nickname === myNickname;
        appendChatMsg(d.nickname, d.text, isSelf);
        break;
      }

      case 'draw':
        drawRemote(msg);
        break;

      case 'clear':
        remoteCtx.clearRect(0, 0, remoteCanvas.width, remoteCanvas.height);
        break;

      case 'round_timer_start': {
        const seconds = msg.data && msg.data.seconds ? msg.data.seconds : 30;
        startRoundTimer(seconds);
        break;
      }

      case 'round_end': {
        const answer = msg.data && msg.data.answer ? msg.data.answer : '???';
        if (msg.data && msg.data.scores) {
          currentScores = msg.data.scores;
          renderScoreboard();
        }
        stopRoundTimer();
        showRoundEnd(answer);
        break;
      }

      case 'score_update': {
        if (msg.data) {
          currentScores = msg.data;
          renderScoreboard();
        }
        break;
      }

      case 'room_settings': {
        if (msg.data && msg.data.target_score) {
          targetScore = msg.data.target_score;
          wrTargetScore = msg.data.target_score;
          updateWrSettingsDisplay();
          updateScoreTargetBadge();
        }
        break;
      }

      case 'game_over': {
        stopRoundTimer();
        const winner = msg.data && msg.data.winner ? msg.data.winner : '';
        const overtime = msg.data && msg.data.overtime;
        const scores = msg.data && msg.data.scores ? msg.data.scores : {};
        currentScores = scores;
        renderScoreboard();
        showGameOver(winner, overtime, scores);
        break;
      }

      case 'error':
        console.warn(`[WS] Error:`, msg.data.message);
        setStatus(msg.data.message || '發生錯誤');
        setLobbyBusy(false);
        if (ws) {
          ws.onclose = null;
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

  // ── Member panel rendering ───────────────────────────────────
  function renderMembers(members) {
    memberCount.textContent = `${members.length} 人`;
    memberList.innerHTML = '';
    members.forEach(m => {
      const li = document.createElement('li');
      const avatar = m.nickname ? m.nickname[0].toUpperCase() : '?';
      const isSelf = m.client_id === clientId;
      let badgesHtml = '';
      if (m.is_host) badgesHtml += '<span class="member-host-badge">👑 房主</span>';
      if (isSelf) badgesHtml += '<span class="member-self-badge">我</span>';
      li.innerHTML = `
        <div class="member-avatar">${avatar}</div>
        <span class="member-name">${m.nickname}</span>
        ${badgesHtml}
      `;
      memberList.appendChild(li);
    });
  }

  // ── Chat helpers ──────────────────────────────────────────
  function appendChatMsg(nickname, text, isSelf) {
    if (!text) return;
    const div = document.createElement('div');
    div.className = 'chat-msg' + (isSelf ? ' self' : '');
    const avatar = nickname ? nickname[0].toUpperCase() : '?';
    const nameClass = 'chat-msg-name' + (isSelf ? ' self' : '');
    div.innerHTML = `
      <div class="chat-msg-header">
        <div class="chat-msg-avatar">${avatar}</div>
        <span class="${nameClass}">${escapeHtml(nickname)}</span>
      </div>
      <div class="chat-msg-bubble">${escapeHtml(text)}</div>
    `;
    chatMessages.appendChild(div);
    // Auto-scroll to bottom
    chatMessages.scrollTop = chatMessages.scrollHeight;
  }

  function appendSystemMsg(text) {
    const div = document.createElement('div');
    div.className = 'chat-system-msg';
    div.textContent = text;
    chatMessages.appendChild(div);
    chatMessages.scrollTop = chatMessages.scrollHeight;
  }

  function escapeHtml(str) {
    return String(str)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;')
      .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }

  function sendChat() {
    const text = chatInput.value.trim();
    if (!text || !currentRoomId) return;
    sendJSON({ event: 'chat', room_id: currentRoomId, data: { text } });
    chatInput.value = '';
    chatInput.focus();
  }

  chatSend.addEventListener('click', sendChat);
  chatInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') sendChat();
  });

  // ── Generic helpers ───────────────────────────────────────
  function sendJSON(obj) {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(obj));
  }

  function setStatus(text) { statusMsg.textContent = text; }
  function setGameStatus(text) { statusMsgGame.textContent = text; }

  // ── Waiting room helpers ──────────────────────────────────
  function showWaitingRoom(roomId) {
    currentRoomId = roomId;
    wrRoomLabel.textContent = roomId;
    lobbyEl.hidden = true;
    waitingRoomEl.hidden = false;
    gameEl.hidden = true;

    // Show correct action button based on host status
    wrReadyBtn.hidden = amIHost;
    wrStartBtn.hidden = !amIHost;
    wrReadyBtn.dataset.ready = 'false';
    wrReadyBtn.querySelector('.wr-ready-text').textContent = '準備好了！';
    wrReadyBtn.querySelector('.wr-ready-icon').textContent = '✅';
    wrStartBtn.disabled = true;
    wrStatus.textContent = '';

    // Settings panel: only host can interact
    if (wrSettingsWrap) {
      if (amIHost) {
        wrSettingsWrap.classList.remove('wr-settings-readonly');
      } else {
        wrSettingsWrap.classList.add('wr-settings-readonly');
      }
    }
    updateWrSettingsDisplay();
  }

  function renderWaitingRoomMembers(members) {
    wrMemberList.innerHTML = '';
    const allNonHostReady = members.every(m => m.is_host || m.is_ready);
    const total = members.length;
    const readyCount = members.filter(m => m.is_host || m.is_ready).length;

    members.forEach(m => {
      const li = document.createElement('li');
      if (m.is_ready || m.is_host) li.classList.add('wr-ready');

      const avatar = document.createElement('div');
      avatar.className = 'wr-member-avatar';
      avatar.textContent = m.nickname ? m.nickname[0].toUpperCase() : '?';

      const name = document.createElement('span');
      name.className = 'wr-member-name';
      name.textContent = m.nickname;

      li.appendChild(avatar);
      li.appendChild(name);

      if (m.is_host) {
        const badge = document.createElement('div');
        badge.className = 'wr-host-label';
        badge.textContent = '👑 房主';
        li.appendChild(badge);
      }

      const statusBadge = document.createElement('span');
      if (m.is_host || m.is_ready) {
        statusBadge.className = 'wr-badge wr-badge-ready wr-badge-left';
        statusBadge.textContent = '✅ 已準備';
      } else {
        statusBadge.className = 'wr-badge wr-badge-waiting wr-badge-left';
        statusBadge.textContent = '⏳ 等待中';
      }
      li.appendChild(statusBadge);

      // Kick button (only visible to host, for non-self non-host members)
      if (amIHost && !m.is_host) {
        const kickBtn = document.createElement('button');
        kickBtn.className = 'wr-kick-btn';
        kickBtn.textContent = '踢出';
        kickBtn.addEventListener('click', () => {
          sendJSON({ event: 'kick_player', room_id: currentRoomId, data: { nickname: m.nickname } });
        });
        li.appendChild(kickBtn);
      }

      wrMemberList.appendChild(li);
    });

    // Update host start button state
    if (amIHost) {
      wrStartBtn.disabled = !allNonHostReady;
      wrStatus.textContent = `${readyCount}/${total} 人已準備`;
    } else {
      wrStatus.textContent = `${readyCount}/${total} 人已準備`;
    }
  }

  // Waiting room: ready button
  wrReadyBtn.addEventListener('click', () => {
    const isReady = wrReadyBtn.dataset.ready === 'true';
    const newReady = !isReady;
    wrReadyBtn.dataset.ready = String(newReady);
    if (newReady) {
      wrReadyBtn.querySelector('.wr-ready-text').textContent = '取消準備';
      wrReadyBtn.querySelector('.wr-ready-icon').textContent = '❌';
    } else {
      wrReadyBtn.querySelector('.wr-ready-text').textContent = '準備好了！';
      wrReadyBtn.querySelector('.wr-ready-icon').textContent = '✅';
    }
    sendJSON({ event: 'player_ready', room_id: currentRoomId, data: {} });
  });

  // Waiting room: start game button (host only)
  wrStartBtn.addEventListener('click', () => {
    sendJSON({ event: 'start_game', room_id: currentRoomId, data: {} });
  });

  // Waiting room: leave button
  wrLeaveBtn.addEventListener('click', () => returnToLobby());

  // Waiting room: score settings (host only)
  function updateWrSettingsDisplay() {
    if (wrTargetScoreDisplay) wrTargetScoreDisplay.textContent = wrTargetScore;
    if (wrScoreGoal) wrScoreGoal.textContent = wrTargetScore;
  }

  if (wrScoreMinus) {
    wrScoreMinus.addEventListener('click', () => {
      if (!amIHost) return;
      wrTargetScore = Math.max(1, wrTargetScore - 1);
      updateWrSettingsDisplay();
      sendJSON({ event: 'set_room_settings', room_id: currentRoomId, data: { target_score: wrTargetScore } });
    });
  }

  if (wrScorePlus) {
    wrScorePlus.addEventListener('click', () => {
      if (!amIHost) return;
      wrTargetScore = Math.min(100, wrTargetScore + 1);
      updateWrSettingsDisplay();
      sendJSON({ event: 'set_room_settings', room_id: currentRoomId, data: { target_score: wrTargetScore } });
    });
  }

  function showGame(roomId, role, topic, drawerNick, isOvertime) {
    currentRoomId = roomId;
    myRole = role || 'guesser';
    resizeCanvases();
    lobbyEl.hidden = true;
    waitingRoomEl.hidden = true;
    gameEl.hidden = false;
    roomLabel.textContent = `房間：${roomId}`;
    setGameStatus(isOvertime ? '加時賽！' : '已連線');

    // Clear all canvas layers for the new round
    localCtx.clearRect(0, 0, localCanvas.width, localCanvas.height);
    remoteCtx.clearRect(0, 0, remoteCanvas.width, remoteCanvas.height);
    previewCtx.clearRect(0, 0, previewCanvas.width, previewCanvas.height);
    remoteStrokes.clear();

    // Hide round-end overlay if visible from previous round
    const roundEndOverlay = document.getElementById('round-end-overlay');
    if (roundEndOverlay) roundEndOverlay.hidden = true;

    // Show role overlay
    const roleOverlay = document.getElementById('role-overlay');
    const roleIcon = document.getElementById('role-icon');
    const roleTitle = document.getElementById('role-title');
    const roleDesc = document.getElementById('role-desc');

    if (myRole === 'drawer') {
      roleIcon.textContent = '🎨';
      roleTitle.textContent = '你是畫家';
      roleDesc.textContent = '請在畫布上作畫，讓其他人猜猜看！';
    } else {
      roleIcon.textContent = '🤔';
      roleTitle.textContent = '你是猜題者';
      const who = drawerNick ? `「${drawerNick}」` : '畫家';
      roleDesc.textContent = `請仔細看${who}畫的內容，並在下方輸入你的答案！`;
    }

    if (window.roleCountdownInterval) {
      clearInterval(window.roleCountdownInterval);
    }

    let countdownValue = 5;
    const countdownDisplay = document.getElementById('countdown-display');
    if (countdownDisplay) countdownDisplay.textContent = countdownValue;
    roleOverlay.hidden = false;

    window.roleCountdownInterval = setInterval(() => {
      countdownValue--;
      if (countdownValue > 0) {
        if (countdownDisplay) countdownDisplay.textContent = countdownValue;
      } else {
        clearInterval(window.roleCountdownInterval);
        roleOverlay.hidden = true;
      }
    }, 1000);

    // Toggle UI elements based on role
    const toolbarLeft = document.getElementById('toolbar-left');
    const topicLabel = document.getElementById('topic-label');
    const topicWord = document.getElementById('topic-word');
    const answerInput = document.getElementById('answer-input');
    const answerSend = document.getElementById('answer-send');

    // Reset guessing state
    document.getElementById('guess-history-list').innerHTML = '';
    document.getElementById('guess-history-panel').hidden = false;
    answerInput.disabled = false;
    answerInput.classList.remove('locked');
    answerSend.disabled = false;
    answerSend.classList.remove('locked');
    answerInput.value = '';

    if (myRole === 'drawer') {
      toolbarLeft.classList.remove('hidden-element');
      topicLabel.hidden = false;
      topicWord.hidden = false;
      if (topic) topicWord.textContent = topic;
      answerInput.hidden = true;
      answerSend.hidden = true;
      localCanvas.style.cursor = currentTool === 'eraser' ? 'cell' : 'crosshair';
    } else {
      toolbarLeft.classList.add('hidden-element');
      topicLabel.hidden = true;
      topicWord.hidden = true;
      answerInput.hidden = false;
      answerSend.hidden = false;
      localCanvas.style.cursor = 'default';
    }
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
    if (myRole === 'drawer') {
      if (tool === 'eraser') {
        localCanvas.style.cursor = 'cell';
      } else {
        localCanvas.style.cursor = 'crosshair';
      }
    } else {
      localCanvas.style.cursor = 'default';
    }
  }

  document.querySelectorAll('.tool-btn').forEach(btn => {
    btn.addEventListener('click', () => selectTool(btn.dataset.tool));
  });

  // ── Toolbar: line width ───────────────────────────────────
  lineWidthInputs.forEach(input => {
    if (!input) return;
    input.addEventListener('input', (e) => {
      lineWidth = parseInt(e.target.value, 10);
      // Sync the other slider
      lineWidthInputs.forEach(other => {
        if (other && other !== e.target) other.value = e.target.value;
      });
    });
  });

  // ── Toolbar: clear button ─────────────────────────────────
  clearBtns.forEach(btn => {
    if (!btn) return;
    btn.addEventListener('click', () => {
      localCtx.clearRect(0, 0, localCanvas.width, localCanvas.height);
      previewCtx.clearRect(0, 0, previewCanvas.width, previewCanvas.height);
      sendClear();
    });
  });

  // ── Answer Input & Guess History ─────────────────────────
  const guessHistoryHeader = document.getElementById('guess-history-header');
  const guessHistoryPanel = document.getElementById('guess-history-panel');
  if (guessHistoryHeader && guessHistoryPanel) {
    guessHistoryHeader.addEventListener('click', () => {
      guessHistoryPanel.classList.toggle('collapsed');
    });
  }

  function submitGuess() {
    if (myRole !== 'guesser') return;
    const answerInput = document.getElementById('answer-input');
    const guessStr = answerInput.value.trim();
    if (!guessStr) return;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({
        event: 'guess',
        room_id: currentRoomId,
        data: { guess: guessStr }
      }));
    }
    answerInput.value = '';
  }

  const answerSend = document.getElementById('answer-send');
  const answerInput = document.getElementById('answer-input');
  if (answerSend) answerSend.addEventListener('click', submitGuess);
  if (answerInput) {
    answerInput.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') submitGuess();
    });
  }

  // ── Lobby UI ──────────────────────────────────────────────

  /** Disable / enable all lobby buttons together */
  function setLobbyBusy(busy) {
    joinBtn.disabled = busy;
    createBtn.disabled = busy;
    roomInput.disabled = busy;
  }

  /** Join an existing room (user types the room ID) */
  joinBtn.addEventListener('click', () => {
    const roomId = roomInput.value.trim();
    if (!roomId) { setStatus('請輸入 6 位數房號'); return; }
    if (!/^\d{6}$/.test(roomId)) { setStatus('房號必須為 6 位數字'); return; }
    myNickname = nickInput.value.trim() || '匿名玩家';
    setLobbyBusy(true);
    connectWebSocket(roomId, 'join_room');
  });

  /** Create a new room with a random 6-digit ID */
  createBtn.addEventListener('click', () => {
    const roomId = String(Math.floor(Math.random() * 900000) + 100000);
    roomInput.value = roomId;
    myNickname = nickInput.value.trim() || '匿名玩家';
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

  /** Room closed by host — OK goes back to lobby */
  roomClosedOk.addEventListener('click', () => {
    roomClosedOverlay.hidden = true;
    returnToLobby();
  });

  // ── Round timer HUD ───────────────────────────────────
  function startRoundTimer(seconds) {
    stopRoundTimer();
    const timerHud = document.getElementById('round-timer-hud');
    const timerCount = document.getElementById('round-timer-count');
    if (!timerHud || !timerCount) return;

    let remaining = seconds;
    timerCount.textContent = remaining;
    timerHud.hidden = false;
    timerHud.classList.remove('timer-urgent');

    roundTimerInterval = setInterval(() => {
      remaining--;
      timerCount.textContent = remaining;
      if (remaining <= 10) {
        timerHud.classList.add('timer-urgent');
      }
      if (remaining <= 0) {
        stopRoundTimer();
      }
    }, 1000);
  }

  function stopRoundTimer() {
    if (roundTimerInterval) {
      clearInterval(roundTimerInterval);
      roundTimerInterval = null;
    }
    const timerHud = document.getElementById('round-timer-hud');
    if (timerHud) {
      timerHud.hidden = true;
      timerHud.classList.remove('timer-urgent');
    }
  }

  // ── Round end overlay ─────────────────────────────────
  function showRoundEnd(answer) {
    const overlay = document.getElementById('round-end-overlay');
    const answerEl = document.getElementById('round-end-answer');
    const bar = document.getElementById('round-end-bar');
    if (!overlay) return;

    if (answerEl) answerEl.textContent = answer;
    overlay.hidden = false;

    // Animate the progress bar over 5 seconds
    if (bar) {
      bar.style.transition = 'none';
      bar.style.width = '100%';
      // Force reflow
      bar.offsetWidth;
      bar.style.transition = 'width 5s linear';
      bar.style.width = '0%';
    }

    // After 5 s, host sends next_round; all clients hide the overlay
    setTimeout(() => {
      overlay.hidden = true;
      if (amIHost && ws && ws.readyState === WebSocket.OPEN) {
        sendJSON({ event: 'next_round', room_id: currentRoomId, data: {} });
      }
    }, 5000);
  }

  // ── Scoreboard rendering ─────────────────────────────────
  function renderScoreboard() {
    const scoreList = document.getElementById('score-list');
    if (!scoreList) return;
    scoreList.innerHTML = '';

    // Sort by score descending
    const sorted = Object.entries(currentScores).sort((a, b) => b[1] - a[1]);
    sorted.forEach(([nick, score], i) => {
      const li = document.createElement('li');
      li.className = 'score-item';
      const medal = i === 0 ? '🥇' : i === 1 ? '🥈' : i === 2 ? '🥉' : `${i + 1}.`;
      const isLeading = score >= targetScore;
      li.innerHTML = `
        <span class="score-rank">${medal}</span>
        <span class="score-nick">${escapeHtml(nick)}</span>
        <span class="score-val${isLeading ? ' score-leading' : ''}">${score}</span>
      `;
      scoreList.appendChild(li);
    });
  }

  function updateScoreTargetBadge() {
    const badge = document.getElementById('score-target-badge');
    if (badge) badge.textContent = `目標:${targetScore}`;
  }

  // ── Game over overlay ─────────────────────────────────
  function showGameOver(winner, overtime, scores) {
    const overlay = document.getElementById('game-over-overlay');
    const titleEl = document.getElementById('game-over-title');
    const subtitleEl = document.getElementById('game-over-subtitle');
    const iconEl = document.getElementById('game-over-icon');
    const scoresEl = document.getElementById('game-over-scores');
    if (!overlay) return;

    if (overtime) {
      iconEl.textContent = '⏰';
      titleEl.textContent = '加時賽！';
      subtitleEl.textContent = '同分！加時賽即將開始，繼續比到出現唯一最高分為止。';
    } else {
      iconEl.textContent = '🏆';
      titleEl.textContent = '遊戲結束！';
      subtitleEl.textContent = `🎉 【${escapeHtml(winner)}】 獲得勝利！`;
    }

    // Final scoreboard
    scoresEl.innerHTML = '';
    const sorted = Object.entries(scores).sort((a, b) => b[1] - a[1]);
    sorted.forEach(([nick, score], i) => {
      const row = document.createElement('div');
      row.className = 'go-score-row' + (nick === winner ? ' go-winner' : '');
      const medal = i === 0 ? '🥇' : i === 1 ? '🥈' : i === 2 ? '🥉' : `${i + 1}.`;
      row.innerHTML = `<span class="go-rank">${medal}</span><span class="go-nick">${escapeHtml(nick)}</span><span class="go-score">${score}</span>`;
      scoresEl.appendChild(row);
    });

    overlay.hidden = false;

    if (overtime) {
      // Auto-dismiss after 4 s for overtime (next round auto-starts via host)
      setTimeout(() => { overlay.hidden = true; }, 4000);
    }
  }

  const gameOverOk = document.getElementById('game-over-ok');
  if (gameOverOk) {
    gameOverOk.addEventListener('click', () => {
      document.getElementById('game-over-overlay').hidden = true;
      returnToLobby();
    });
  }

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
    amIHost = false;
    isPainting = false;
    shapeStart = null;
    remoteStrokes.clear();
    stopRoundTimer();
    currentScores = {};
    targetScore = 10;
    wrTargetScore = 10;
    const roundEndOverlay = document.getElementById('round-end-overlay');
    if (roundEndOverlay) roundEndOverlay.hidden = true;
    const gameOverOverlay = document.getElementById('game-over-overlay');
    if (gameOverOverlay) gameOverOverlay.hidden = true;

    // Reset toolbar to defaults
    selectTool('pen');
    currentColor = '#1a1a2e';
    lineWidth = 3;
    lineWidthInputs.forEach(inp => { if (inp) inp.value = 3; });
    document.querySelectorAll('.color-swatch').forEach((b, i) => {
      b.classList.toggle('selected', i === 0);
    });

    // Reset lobby
    roomInput.value = '';
    setStatus('');
    setLobbyBusy(false);
    // Clear member panel
    memberList.innerHTML = '';
    memberCount.textContent = '0 人';
    wrMemberList.innerHTML = '';
    // Ensure modals are hidden
    roomClosedOverlay.hidden = true;
    confirmOverlay.hidden = true;
    // Clear chat
    chatMessages.innerHTML = '';

    // Switch screens
    gameEl.hidden = true;
    waitingRoomEl.hidden = true;
    lobbyEl.hidden = false;
  }

  console.log('[app.js] loaded — clientId:', clientId);
})();
