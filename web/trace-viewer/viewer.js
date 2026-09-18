// GoTorrent Trace Viewer — replays a Phase 8 -trace JSONL file entirely in
// the browser. No build step, no dependencies, no network access: a trace
// file can contain real IP addresses, so this deliberately never sends
// anything anywhere — FileReader only, opened straight off disk.
//
// State model: rather than maintaining incremental state as the scrubber
// moves, seekTo() replays every event from 0 up to the target index each
// time and rebuilds piece/peer/choke state from scratch. This is the
// simplest thing that is obviously correct (no risk of state drifting from
// what "undo" would have produced), and for the event counts a real trace
// actually produces (thousands to tens of thousands of lines for a
// real download) a full replay costs low single-digit milliseconds — not
// worth the bug surface of an incremental alternative. If a future trace
// ever gets large enough for this to matter, that is the point to revisit
// it, not before.

(() => {
  'use strict';

  const KIND = {
    STATE: 'state_changed',
    PEER_CONNECTED: 'peer_connected',
    PEER_DISCONNECTED: 'peer_disconnected',
    CHOKE: 'choke',
    UNCHOKE: 'unchoke',
    REQUEST: 'piece_request',
    BLOCK: 'block_received',
    VERIFIED: 'piece_verified',
    PICKER: 'picker_decision',
  };

  // --- DOM handles ----------------------------------------------------

  const fileInput = document.getElementById('file-input');
  const fileLabel = document.getElementById('file-label');
  const torrentSelect = document.getElementById('torrent-select');
  const loadStatus = document.getElementById('load-status');
  const app = document.getElementById('app');

  const scrubber = document.getElementById('scrubber');
  const positionLabel = document.getElementById('position-label');
  const nowLabel = document.getElementById('now-label');
  const btnPlay = document.getElementById('btn-play');
  const btnStepBack = document.getElementById('btn-step-back');
  const btnStepFwd = document.getElementById('btn-step-fwd');
  const speedSelect = document.getElementById('speed-select');

  const pieceCanvas = document.getElementById('piece-map-canvas');
  const pieceLegend = document.getElementById('piece-map-legend');
  const swarmCanvas = document.getElementById('swarm-canvas');
  const chokeCanvas = document.getElementById('choke-canvas');
  const contributionBars = document.getElementById('contribution-bars');
  const eventLog = document.getElementById('event-log');

  // --- data (all events, per torrent) ----------------------------------

  let eventsByTorrent = new Map(); // infohash -> Event[]
  let events = [];                 // the currently-selected torrent's events
  let index = -1;                  // last replayed event index (inclusive), -1 = nothing replayed
  let numPieces = 0;
  let playTimer = null;

  // --- file loading ------------------------------------------------------

  fileInput.addEventListener('change', () => {
    const file = fileInput.files[0];
    if (!file) return;
    fileLabel.textContent = file.name;
    loadStatus.textContent = 'Reading...';
    loadStatus.className = 'status';

    const reader = new FileReader();
    reader.onload = () => {
      try {
        loadTrace(reader.result);
      } catch (err) {
        loadStatus.textContent = 'Failed to parse: ' + err.message;
        loadStatus.className = 'status error';
      }
    };
    reader.onerror = () => {
      loadStatus.textContent = 'Could not read file: ' + reader.error;
      loadStatus.className = 'status error';
    };
    reader.readAsText(file);
  });

  function loadTrace(text) {
    eventsByTorrent = new Map();
    let malformed = 0;
    let total = 0;

    for (const line of text.split('\n')) {
      const trimmed = line.trim();
      if (!trimmed) continue;
      total++;
      let ev;
      try {
        ev = JSON.parse(trimmed);
      } catch {
        malformed++;
        continue;
      }
      if (typeof ev.torrent !== 'string' || typeof ev.kind !== 'string') {
        malformed++;
        continue;
      }
      if (!eventsByTorrent.has(ev.torrent)) eventsByTorrent.set(ev.torrent, []);
      eventsByTorrent.get(ev.torrent).push(ev);
    }

    if (eventsByTorrent.size === 0) {
      loadStatus.textContent = `No recognizable trace events found (${total} line(s) read, ${malformed} malformed).`;
      loadStatus.className = 'status error';
      app.classList.add('hidden');
      return;
    }

    // Events for one torrent can interleave with events the actor emitted
    // for another torrent this same fleet was managing, but a single
    // torrent's own events are always written in the order its actor
    // produced them — no re-sort needed within one torrent's slice.

    torrentSelect.innerHTML = '';
    for (const hash of eventsByTorrent.keys()) {
      const opt = document.createElement('option');
      opt.value = hash;
      opt.textContent = `${hash} (${eventsByTorrent.get(hash).length} events)`;
      torrentSelect.appendChild(opt);
    }
    torrentSelect.disabled = false;

    const skipped = malformed > 0 ? `, ${malformed} line(s) skipped` : '';
    loadStatus.textContent = `Loaded ${total} event(s) across ${eventsByTorrent.size} torrent(s)${skipped}.`;
    loadStatus.className = 'status';

    app.classList.remove('hidden');
    selectTorrent(torrentSelect.value);
  }

  torrentSelect.addEventListener('change', () => selectTorrent(torrentSelect.value));

  function selectTorrent(hash) {
    events = eventsByTorrent.get(hash) || [];
    numPieces = 0;
    for (const ev of events) {
      if (typeof ev.piece === 'number' && ev.piece + 1 > numPieces) numPieces = ev.piece + 1;
    }
    scrubber.max = Math.max(0, events.length - 1);
    buildLogRows();
    seekTo(events.length - 1); // start fully replayed — "what does this download look like at the end"
  }

  // --- replay state, rebuilt on every seek ------------------------------

  function freshState() {
    return {
      torrentState: 'unknown',
      pieces: new Array(numPieces).fill('missing'), // 'missing' | 'requested' | 'have'
      pieceOwner: new Array(numPieces).fill(null),   // peer addr that delivered it, or null
      // peers: addr -> { connected, connectedAt, chokedByUs, downloaded, chokeSpans: [{from, to, choked}] }
      peers: new Map(),
    };
  }

  function ensurePeer(state, addr) {
    if (!state.peers.has(addr)) {
      state.peers.set(addr, {
        connected: true,
        downloaded: 0,
        chokedByUs: true, // BEP 3: both sides start choked
        chokeSpans: [],
        chokeSpanStart: 0,
      });
    }
    return state.peers.get(addr);
  }

  function closeChokeSpan(peer, atIndex) {
    peer.chokeSpans.push({ from: peer.chokeSpanStart, to: atIndex, choked: peer.chokedByUs });
    peer.chokeSpanStart = atIndex;
  }

  function applyEvent(state, ev, atIndex) {
    switch (ev.kind) {
      case KIND.STATE:
        state.torrentState = ev.to || state.torrentState;
        break;
      case KIND.PEER_CONNECTED: {
        const p = ensurePeer(state, ev.peer);
        p.connected = true;
        p.chokeSpanStart = atIndex;
        break;
      }
      case KIND.PEER_DISCONNECTED: {
        const p = state.peers.get(ev.peer);
        if (p) {
          closeChokeSpan(p, atIndex);
          p.connected = false;
        }
        break;
      }
      case KIND.CHOKE: {
        const p = ensurePeer(state, ev.peer);
        closeChokeSpan(p, atIndex);
        p.chokedByUs = true;
        break;
      }
      case KIND.UNCHOKE: {
        const p = ensurePeer(state, ev.peer);
        closeChokeSpan(p, atIndex);
        p.chokedByUs = false;
        break;
      }
      case KIND.REQUEST:
        if (numPieces && ev.piece < numPieces && state.pieces[ev.piece] === 'missing') {
          state.pieces[ev.piece] = 'requested';
        }
        break;
      case KIND.BLOCK: {
        const p = ensurePeer(state, ev.peer);
        p.downloaded += ev.length || 0;
        break;
      }
      case KIND.VERIFIED:
        if (numPieces && ev.piece < numPieces) {
          state.pieces[ev.piece] = ev.ok ? 'have' : 'missing';
          if (ev.ok) state.pieceOwner[ev.piece] = ev.peer || null;
        }
        break;
      // picker_decision carries no replay-state of its own — it only
      // annotates the event log with the picker's reasoning.
    }
  }

  function seekTo(target) {
    target = Math.max(-1, Math.min(events.length - 1, target));
    const state = freshState();
    for (let i = 0; i <= target; i++) applyEvent(state, events[i], i);
    // Close every still-open choke span at the current position so the
    // timeline renders a span all the way up to "now" for a connected peer.
    for (const p of state.peers.values()) {
      if (p.chokeSpanStart <= target) {
        p.chokeSpans.push({ from: p.chokeSpanStart, to: target, choked: p.chokedByUs });
      }
    }
    index = target;
    render(state);
  }

  // --- rendering -----------------------------------------------------

  function peerColor(addr) {
    // FNV-1a-ish hash -> hue, so the same peer address always gets the
    // same color within one trace and across the piece map / swarm /
    // contribution panels, the same "deterministic per-peer color" idea
    // Desktop's own piece-map-with-peer-attribution feature already uses.
    let h = 2166136261;
    for (let i = 0; i < addr.length; i++) {
      h ^= addr.charCodeAt(i);
      h = Math.imul(h, 16777619);
    }
    const hue = Math.abs(h) % 360;
    return `hsl(${hue}, 70%, 55%)`;
  }

  function render(state) {
    positionLabel.textContent = `${index + 1} / ${events.length}`;
    scrubber.value = String(Math.max(0, index));
    const firstTs = events.length && events[0].time ? new Date(events[0].time).getTime() : 0;
    const cur = events[index];
    nowLabel.textContent = cur ? `state: ${state.torrentState} — ${formatTime(cur, firstTs)}` : '';

    renderPieceMap(state);
    renderSwarm(state);
    renderContribution(state);
    renderChokeTimeline(state);
    highlightLog();
  }

  function sizeCanvasToDisplay(canvas) {
    const rect = canvas.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    const w = Math.max(1, Math.round(rect.width * dpr));
    const h = Math.max(1, Math.round(rect.height * dpr));
    if (canvas.width !== w || canvas.height !== h) {
      canvas.width = w;
      canvas.height = h;
    }
    const ctx = canvas.getContext('2d');
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    return { ctx, width: rect.width, height: rect.height };
  }

  function renderPieceMap(state) {
    const { ctx, width, height } = sizeCanvasToDisplay(pieceCanvas);
    ctx.clearRect(0, 0, width, height);

    if (numPieces === 0) {
      ctx.fillStyle = '#8a94a6';
      ctx.font = '12px sans-serif';
      ctx.fillText('No piece-level events in this trace (a trace with only state/peer events, no downloading).', 8, 20);
      return;
    }

    const cols = Math.max(1, Math.floor(width / 10));
    const cellW = width / cols;
    const rows = Math.ceil(numPieces / cols);
    const cellH = Math.min(14, height / rows);

    for (let i = 0; i < numPieces; i++) {
      const col = i % cols;
      const row = Math.floor(i / cols);
      const x = col * cellW;
      const y = row * cellH;
      const status = state.pieces[i];
      let color = '#2c3543'; // missing
      if (status === 'requested') color = '#e0a63a';
      else if (status === 'have') color = state.pieceOwner[i] ? peerColor(state.pieceOwner[i]) : '#34d27a';
      ctx.fillStyle = color;
      ctx.fillRect(x, y, Math.max(1, cellW - 1), Math.max(1, cellH - 1));
    }

    const have = state.pieces.filter((s) => s === 'have').length;
    pieceLegend.innerHTML = `
      <span><span class="swatch" style="background:#2c3543"></span>missing</span>
      <span><span class="swatch" style="background:#e0a63a"></span>requested</span>
      <span><span class="swatch" style="background:#34d27a"></span>have (color = delivering peer, when known)</span>
      <span>${have} / ${numPieces} pieces</span>
    `;
  }

  function renderSwarm(state) {
    const { ctx, width, height } = sizeCanvasToDisplay(swarmCanvas);
    ctx.clearRect(0, 0, width, height);

    const peers = [...state.peers.entries()];
    if (peers.length === 0) {
      ctx.fillStyle = '#8a94a6';
      ctx.font = '12px sans-serif';
      ctx.fillText('No peers connected at this point in the trace.', 8, 20);
      return;
    }

    const cx = width / 2;
    const cy = height / 2;
    const radius = Math.min(cx, cy) - 40;
    const maxBytes = Math.max(1, ...peers.map(([, p]) => p.downloaded));

    // "us" at the center
    ctx.fillStyle = '#00c6ff';
    ctx.beginPath();
    ctx.arc(cx, cy, 10, 0, Math.PI * 2);
    ctx.fill();

    peers.forEach(([addr, p], i) => {
      const angle = (i / peers.length) * Math.PI * 2 - Math.PI / 2;
      const x = cx + Math.cos(angle) * radius;
      const y = cy + Math.sin(angle) * radius;

      ctx.strokeStyle = p.connected ? (p.chokedByUs ? '#e0a63a' : '#34d27a') : '#3a4353';
      ctx.setLineDash(p.connected ? [] : [4, 3]);
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.moveTo(cx, cy);
      ctx.lineTo(x, y);
      ctx.stroke();
      ctx.setLineDash([]);

      const nodeRadius = 4 + 8 * (p.downloaded / maxBytes);
      ctx.fillStyle = p.connected ? peerColor(addr) : '#3a4353';
      ctx.beginPath();
      ctx.arc(x, y, nodeRadius, 0, Math.PI * 2);
      ctx.fill();

      ctx.fillStyle = p.connected ? '#e4e8ef' : '#8a94a6';
      ctx.font = '10px monospace';
      const label = addr.length > 21 ? addr.slice(0, 19) + '…' : addr;
      ctx.textAlign = x < cx ? 'right' : 'left';
      ctx.fillText(label, x + (x < cx ? -nodeRadius - 4 : nodeRadius + 4), y + 3);
      ctx.textAlign = 'left';
    });
  }

  function renderContribution(state) {
    const peers = [...state.peers.entries()].sort((a, b) => b[1].downloaded - a[1].downloaded);
    contributionBars.innerHTML = '';
    if (peers.length === 0) {
      contributionBars.innerHTML = '<div class="empty-note">No blocks received yet at this point.</div>';
      return;
    }
    const maxBytes = Math.max(1, ...peers.map(([, p]) => p.downloaded));
    for (const [addr, p] of peers) {
      const row = document.createElement('div');
      row.className = 'contribution-row';
      const pct = Math.round((p.downloaded / maxBytes) * 100);
      row.innerHTML = `
        <span class="addr" title="${addr}">${addr}</span>
        <span class="bar-track"><span class="bar-fill" style="width:${pct}%;background:${peerColor(addr)}"></span></span>
        <span class="bytes">${formatBytes(p.downloaded)}</span>
      `;
      contributionBars.appendChild(row);
    }
  }

  function renderChokeTimeline(state) {
    const { ctx, width, height } = sizeCanvasToDisplay(chokeCanvas);
    ctx.clearRect(0, 0, width, height);

    const peers = [...state.peers.entries()];
    if (peers.length === 0 || events.length === 0) {
      ctx.fillStyle = '#8a94a6';
      ctx.font = '12px sans-serif';
      ctx.fillText('No peers to show yet.', 8, 20);
      return;
    }

    const rowH = Math.min(20, height / peers.length);
    const total = Math.max(1, events.length - 1);

    peers.forEach(([addr, p], row) => {
      const y = row * rowH;
      for (const span of p.chokeSpans) {
        const x1 = (span.from / total) * width;
        const x2 = (span.to / total) * width;
        ctx.fillStyle = span.choked ? '#e0a63a' : '#34d27a';
        ctx.fillRect(x1, y + 2, Math.max(1, x2 - x1), rowH - 4);
      }
      if (!p.connected) {
        ctx.fillStyle = 'rgba(20,24,31,0.55)';
        const lastTo = p.chokeSpans.length ? (p.chokeSpans[p.chokeSpans.length - 1].to / total) * width : 0;
        ctx.fillRect(lastTo, y + 2, width - lastTo, rowH - 4);
      }
      ctx.fillStyle = '#e4e8ef';
      ctx.font = '9px monospace';
      const label = addr.length > 24 ? addr.slice(0, 22) + '…' : addr;
      ctx.fillText(label, 4, y + rowH / 2 + 3);
    });
  }

  // --- event log ---------------------------------------------------------

  function describeEvent(ev) {
    const peer = ev.peer ? ` ${ev.peer}` : '';
    switch (ev.kind) {
      case KIND.STATE:
        return `State: ${ev.from} → ${ev.to}`;
      case KIND.PEER_CONNECTED:
        return `Peer connected:${peer}`;
      case KIND.PEER_DISCONNECTED:
        return `Peer disconnected:${peer}`;
      case KIND.CHOKE:
        return `Choked upload to${peer}`;
      case KIND.UNCHOKE:
        return `Unchoked upload to${peer}`;
      case KIND.REQUEST:
        return `Requested piece ${ev.piece} [${ev.begin},${ev.begin + ev.length}) from${peer}`;
      case KIND.BLOCK:
        return `Received piece ${ev.piece} [${ev.begin},${ev.begin + ev.length}) (${ev.length} B) from${peer}`;
      case KIND.VERIFIED:
        return ev.ok
          ? `Piece ${ev.piece} verified OK${peer ? ' (delivered by' + peer + ')' : ''}`
          : `Piece ${ev.piece} FAILED hash check${ev.err ? ': ' + ev.err : ''}`;
      case KIND.PICKER:
        return `Picker started piece ${ev.piece} (priority ${ev.priority}, ${ev.strategy}, rarity ${ev.rarity}${ev.endgame ? ', endgame' : ''})`;
      default:
        return ev.kind;
    }
  }

  function formatTime(ev, firstTs) {
    if (!ev.time) return '';
    const t = new Date(ev.time).getTime();
    const dt = (t - firstTs) / 1000;
    return `T+${dt.toFixed(3)}s`;
  }

  let logRowEls = [];

  function buildLogRows() {
    eventLog.innerHTML = '';
    logRowEls = [];
    if (events.length === 0) return;
    const firstTs = events[0].time ? new Date(events[0].time).getTime() : 0;
    const frag = document.createDocumentFragment();
    events.forEach((ev, i) => {
      const row = document.createElement('div');
      row.className = `log-row kind-${ev.kind}${ev.kind === KIND.VERIFIED ? (ev.ok ? ' ok' : ' fail') : ''}`;
      row.innerHTML = `<span class="t">${formatTime(ev, firstTs)}</span>${escapeHtml(describeEvent(ev))}`;
      row.addEventListener('click', () => seekTo(i));
      frag.appendChild(row);
      logRowEls.push(row);
    });
    eventLog.appendChild(frag);
  }

  function highlightLog() {
    logRowEls.forEach((row, i) => {
      row.classList.toggle('current', i === index);
      row.classList.toggle('future', i > index);
    });
    const current = logRowEls[index];
    if (current) current.scrollIntoView({ block: 'nearest' });
  }

  function escapeHtml(s) {
    return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  }

  function formatBytes(n) {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
    return `${(n / (1024 * 1024)).toFixed(2)} MiB`;
  }

  // --- transport controls -------------------------------------------------

  scrubber.addEventListener('input', () => seekTo(Number(scrubber.value)));
  btnStepBack.addEventListener('click', () => { pause(); seekTo(index - 1); });
  btnStepFwd.addEventListener('click', () => { pause(); seekTo(index + 1); });
  btnPlay.addEventListener('click', () => (playTimer ? pause() : play()));

  function play() {
    if (events.length === 0) return;
    if (index >= events.length - 1) seekTo(-1); // replay from the start if already at the end
    btnPlay.textContent = '⏸'; // pause glyph
    const tick = () => {
      if (index >= events.length - 1) {
        pause();
        return;
      }
      seekTo(index + 1);
      const rate = Number(speedSelect.value);
      playTimer = setTimeout(tick, 1000 / rate);
    };
    tick();
  }

  function pause() {
    if (playTimer) clearTimeout(playTimer);
    playTimer = null;
    btnPlay.textContent = '▶'; // play glyph
  }

  window.addEventListener('resize', () => {
    if (index >= 0) seekTo(index); // re-render at the same position against the new canvas size
  });
})();
