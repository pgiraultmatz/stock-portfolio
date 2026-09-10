let chartCycleMode = false;
let btcCycleResult = null;
let btcCycleRequest = 0;
let btcCycleAbort = null;
let btcCycleHover = null;
let btcCycleFrame = null;

function syncBTCChartMode(symbol) {
  const available = symbol === 'BTC-USD';
  document.getElementById('btc-mode-tabs').hidden = !available;
  if (!available) chartCycleMode = false;
  document.getElementById('btc-cycle-tools').hidden = !chartCycleMode;
  document.querySelectorAll('#btc-mode-tabs button').forEach((button, index) => button.setAttribute('aria-selected', String(index === (chartCycleMode ? 1 : 0))));
  for (const id of ['charts-range', 'charts-interval', 'charts-ma-mode', 'charts-fib-toggle']) document.getElementById(id).disabled = chartCycleMode;
  if (!chartCycleMode) {
    btcCycleAbort?.abort();
    btcCycleRequest++;
    btcCycleHover = null;
  }
  return chartCycleMode;
}

function setBTCChartMode(enabled) {
  if (enabled && chartLastSymbol !== 'BTC-USD') return;
  chartCycleMode = enabled;
  chartFrame = null;
  chartHover = null;
  loadChart();
}

function selectBTCCyclePreset() {
  const historical = document.getElementById('btc-cycle-preset').value === 'historical';
  document.getElementById('btc-cycle-reference').value = historical ? '2015-01-14' : '2018-12-15';
  document.getElementById('btc-cycle-target').value = historical ? '2018-12-15' : '2022-11-21';
  document.getElementById('btc-cycle-asof').value = historical ? '2021-06-01' : btcYesterday();
  loadChart();
}

function btcYesterday() { return new Date(Date.now() - 86400000).toISOString().slice(0, 10); }
function btcDate(time) { return new Date(time * 1000).toLocaleDateString('fr-FR', { timeZone: 'UTC' }); }
function btcMoney(value) { return new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', maximumFractionDigits: 0 }).format(value); }

function drawBTCHalfMonthBands(ctx, points, frame) {
  const observed = points.filter(p => Number.isFinite(p.time) && Number.isFinite(p.value)).sort((a, b) => a.time - b.time);
  if (!observed.length) return;
  const first = new Date(observed[0].time * 1000);
  const last = new Date(observed[observed.length - 1].time * 1000);
  let cursor = new Date(Date.UTC(first.getUTCFullYear(), first.getUTCMonth(), 14));
  if (cursor.getTime() / 1000 < observed[0].time) cursor.setUTCMonth(cursor.getUTCMonth() + 1);
  while (cursor.getTime() / 1000 <= observed[observed.length - 1].time) {
    const end = new Date(Date.UTC(cursor.getUTCFullYear(), cursor.getUTCMonth() + 1, 0, 23, 59, 59));
    const startTime = cursor.getTime() / 1000;
    const endTime = end.getTime() / 1000;
    const startPoint = observed.find(p => p.time >= startTime);
    const period = observed.filter(p => p.time >= startTime && p.time <= endTime);
    const endPoint = period[period.length - 1];
    if (startPoint && endPoint && endPoint.time > startPoint.time) {
      const bullish = endPoint.value >= startPoint.value;
      ctx.fillStyle = bullish ? 'rgba(63,185,80,.10)' : 'rgba(248,81,73,.10)';
      ctx.fillRect(frame.x(startPoint.time), frame.pad.top, Math.max(1, frame.x(endPoint.time) - frame.x(startPoint.time)), frame.bottom - frame.pad.top);
      ctx.strokeStyle = bullish ? 'rgba(63,185,80,.42)' : 'rgba(248,81,73,.42)';
      ctx.setLineDash([3, 4]);
      ctx.beginPath(); ctx.moveTo(frame.x(startPoint.time), frame.pad.top); ctx.lineTo(frame.x(startPoint.time), frame.bottom); ctx.stroke();
      ctx.setLineDash([]);
    }
    cursor.setUTCMonth(cursor.getUTCMonth() + 1);
  }
}

async function loadBTCCycle(options = {}) {
  btcCycleAbort?.abort();
  const controller = new AbortController();
  btcCycleAbort = controller;
  const request = ++btcCycleRequest;
  btcCycleResult = null;
  btcCycleFrame = null;
  btcCycleHover = null;
  chartFrame = null;
  document.getElementById('charts-analysis').classList.remove('show');
  document.getElementById('charts-meta').textContent = 'BTC-USD · Cycles · Clôtures quotidiennes · Échelle logarithmique';
  document.getElementById('btc-cycle-summary').textContent = 'Calcul en cours…';
  const asof = document.getElementById('btc-cycle-asof');
  asof.max = btcYesterday();
  if (!asof.value) asof.value = asof.max;
  const params = new URLSearchParams({
    referenceStart: document.getElementById('btc-cycle-reference').value,
    targetStart: document.getElementById('btc-cycle-target').value,
    asOf: asof.value,
    horizon: document.getElementById('btc-cycle-horizon').value,
  });
  if (options.refresh) params.set('refresh', '1');
  drawBTCCycle();
  try {
    const response = await fetch(`/api/btc-cycles?${params}`, { signal: controller.signal });
    if (!response.ok) throw new Error((await response.text()).trim());
    const result = await response.json();
    if (request !== btcCycleRequest || !chartCycleMode) return;
    btcCycleResult = result;
    renderBTCCycleSummary(result);
    drawBTCCycle();
  } catch (error) {
    if (error.name === 'AbortError' || request !== btcCycleRequest || !chartCycleMode) return;
    document.getElementById('btc-cycle-summary').textContent = `Calcul indisponible : ${error.message}`;
    document.getElementById('charts-meta').textContent = 'BTC-USD · Cycles · Données indisponibles';
  }
}

function renderBTCCycleSummary(result) {
  const f = result.fit;
  const last = result.actual[result.actual.length - 1];
  const days = Math.round((result.asOf - result.targetStart.time) / 86400);
  const drawdown = (last.value / result.observedHigh.value - 1) * 100;
  const stats = [
    ['Vitesse ajustée', `${f.speed.toFixed(2)}×`],
    ['Amplitude logarithmique', `${f.amplitude.toFixed(2)}×`],
    ['Erreur log, ajustement', f.logRMSE.toFixed(3)],
    ['Depuis l’ancrage', `${days} jours`],
    ['Écart au plus haut observé', `${drawdown.toFixed(1)} %`],
    ['ATH de référence (clôture)', `${btcMoney(result.referenceATH.value)} · ${btcDate(result.referenceATH.time)}`],
  ];
  const validation = result.validation.map(v => `<tr><td>${v.horizon} jours</td><td>${v.samples}</td><td>${v.samples ? v.modelLogMAE.toFixed(3) : '—'}</td><td>${v.samples ? v.flatLogMAE.toFixed(3) : '—'}</td></tr>`).join('');
  const comparison = result.validation.filter(v => v.samples > 0);
  const verdict = !comparison.length ? 'Recul insuffisant pour évaluer les projections.' : comparison.every(v => v.modelLogMAE < v.flatLogMAE) ? 'Erreur inférieure au prix inchangé sur ces tests conditionnels ; généralisation non démontrée.' : 'Le modèle ne bat pas systématiquement la référence prix inchangé.';
  const forecast = result.topForecast;
  const bottom = result.bottom || {};
  const eventsHtml = (result.events || []).map(event => `<div>${esc(event.label)}<strong>${btcMoney(event.point.value)}</strong><span class="charts-meta">${btcDate(event.point.time)} · ${esc(event.cycle)}</span></div>`).join('');
  const bottomStatus = bottom.twoRedSixMonthCandles ? 'Bottom probable' : (bottom.status || 'Indisponible');
  const bottomHtml = `<div class="btc-cycle-summary">
    <div>Règle des bottoms<strong>${bottomStatus}</strong><span class="charts-meta">Deux bougies 6M rouges : ${bottom.twoRedSixMonthCandles ? 'oui · portion en rouge' : 'non'}</span></div>
    <div>Bottom candidat<strong>${bottom.candidateBottom ? btcMoney(bottom.candidateBottom.value) : '—'}</strong><span class="charts-meta">${bottom.candidateBottom ? btcDate(bottom.candidateBottom.time) : 'Aucun candidat mesuré'}</span></div>
    <div>Drawdown mesuré<strong>${Number.isFinite(bottom.drawdown) ? (bottom.drawdown * 100).toFixed(1) + ' %' : '—'}</strong><span class="charts-meta">Confirmation indépendante requise</span></div>
  </div>`;
  const forecastHtml = forecast ? `
    <div class="btc-cycle-summary">
      <div>ATH récent<strong>${btcMoney(forecast.latestATH.value)}</strong><span class="charts-meta">${btcDate(forecast.latestATH.time)}</span></div>
      <div>Gain du cycle précédent<strong>${(forecast.previousCycleGain * 100).toFixed(0)} %</strong><span class="charts-meta">ATH précédent : ${btcMoney(forecast.previousATH.value)}</span></div>
      <div>Facteur de rendements décroissants<strong>${forecast.diminishingFactor.toFixed(4)}</strong><span class="charts-meta">Hypothèse Killa</span></div>
      <div>Franchissement du précédent ATH<strong>${btcDate(forecast.breakoutDate)}</strong><span class="charts-meta">cible de reprise du record 2025</span></div>
      <div>Sommet de cycle hypothétique<strong>${btcMoney(forecast.priceCentral)}</strong><span class="charts-meta">Zone ${btcMoney(forecast.priceLow)} – ${btcMoney(forecast.priceHigh)}</span></div>
      <div>Fenêtre basse du sommet<strong>${btcDate(forecast.dateLow)}</strong><span class="charts-meta">borne basse temporelle</span></div>
      <div>Fenêtre centrale du sommet<strong>${btcDate(forecast.dateCentral)}</strong><span class="charts-meta">borne haute : ${btcDate(forecast.dateHigh)}</span></div>
    </div>
    <p class="btc-cycle-note">${esc(forecast.method)}</p>` : '<p class="btc-cycle-note">Prévision ATH indisponible : il manque un ATH historique antérieur à l’ancrage de référence.</p>';
  document.getElementById('btc-cycle-summary').innerHTML = `
    <div class="btc-cycle-summary">${eventsHtml}</div>
    ${bottomHtml}
    ${forecastHtml}
    <div class="btc-cycle-summary">${stats.map(([label, value]) => `<div>${esc(label)}<strong>${esc(value)}</strong></div>`).join('')}</div>
    <div class="charts-meta">Clôture observée : ${btcDate(result.asOf)} · Reconquête de l’ATH de référence : ${result.reclaimedATH ? btcDate(result.reclaimedATH.time) : 'non observée'} · Source Yahoo : ${esc(new Date(result.updatedAt).toLocaleString('fr-FR'))}</div>
    <table class="btc-cycle-validation"><thead><tr><th>Tests chronologiques</th><th>Origines</th><th>Erreur log modèle</th><th>Prix inchangé</th></tr></thead><tbody>${validation}</tbody></table>
    <p class="btc-cycle-note">${esc(verdict)}</p>
    ${(result.warnings || []).map(w => `<p class="btc-cycle-note">${esc(w)}</p>`).join('')}`;
}

function drawBTCCycle() {
  if (!chartCycleMode) return;
  const canvas = document.getElementById('charts-canvas');
  const empty = document.getElementById('charts-empty');
  empty.style.display = 'none';
  const w = Math.max(280, canvas.parentElement.clientWidth);
  const h = Math.max(320, canvas.parentElement.clientHeight);
  const dpr = window.devicePixelRatio || 1;
  canvas.width = Math.floor(w * dpr);
  canvas.height = Math.floor(h * dpr);
  canvas.style.width = `${w}px`;
  canvas.style.height = `${h}px`;
  const ctx = canvas.getContext('2d');
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  const result = btcCycleResult;
  if (!result) return;
  const pad = { left: 12, right: 82, top: 28, bottom: 38 };
  const right = w - pad.right, bottom = h - pad.bottom;
  const all = [...(result.previous || []), ...result.actual, ...result.aligned, ...(result.nextCycle || []), ...(result.nextCycleRecent || []), ...(result.scenarios || []).flatMap(s => s.points), ...(result.events || []).map(e => e.point)];
  let low = Infinity, high = -Infinity, end = result.asOf;
  for (const p of all) {
    if (!Number.isFinite(p.value) || p.value <= 0) continue;
    low = Math.min(low, Math.log(p.value)); high = Math.max(high, Math.log(p.value)); end = Math.max(end, p.time);
  }
  const margin = Math.max(.01, high - low) * .1;
  low -= margin; high += margin;
  // Keep the previous cycle's anchor events on the same timeline as the
  // current and projected cycles so the full cycle transition is visible.
  const eventTimes = (result.events || [])
    .map((event) => Number(event.point?.time))
    .filter(Number.isFinite);
  const start = Math.min(result.targetStart.time, ...eventTimes);
  const x = t => pad.left + (t - start) / Math.max(86400, end - start) * (right - pad.left);
  const y = value => pad.top + (high - Math.log(value)) / (high - low) * (bottom - pad.top);
  btcCycleFrame = { x, y, start, end, low, high, pad, right, bottom, w, h };
  drawBTCHalfMonthBands(ctx, [...(result.previous || []), ...result.actual], btcCycleFrame);
  ctx.font = '11px monospace';
  ctx.textAlign = 'left';
  for (let i = 0; i <= 5; i++) {
    const yy = pad.top + i / 5 * (bottom - pad.top);
    ctx.strokeStyle = '#21262d'; ctx.beginPath(); ctx.moveTo(pad.left, yy); ctx.lineTo(right, yy); ctx.stroke();
    ctx.fillStyle = '#8b949e'; ctx.fillText(btcMoney(Math.exp(high - i / 5 * (high - low))), right + 6, yy + 4);
  }
  const tickCount = Math.max(2, Math.floor((right - pad.left) / 130));
  ctx.textAlign = 'center';
  for (let i = 0; i <= tickCount; i++) {
    const t = start + i / tickCount * (end - start);
    const label = new Date(t * 1000).toLocaleDateString('fr-FR', { month: 'short', year: '2-digit', timeZone: 'UTC' });
    ctx.fillText(label, Math.max(36, Math.min(right - 20, x(t))), bottom + 24);
  }
  ctx.save(); ctx.beginPath(); ctx.rect(pad.left, pad.top, right - pad.left, bottom - pad.top); ctx.clip();
  ctx.fillStyle = 'rgba(227,179,65,.035)'; ctx.fillRect(x(result.asOf), pad.top, right - x(result.asOf), bottom - pad.top);
  const line = (points, color, dash, width = 1.6) => {
    ctx.strokeStyle = color; ctx.lineWidth = width; ctx.setLineDash(dash); ctx.beginPath();
    points.forEach((p, i) => { if (i === 0) ctx.moveTo(x(p.time), y(p.value)); else ctx.lineTo(x(p.time), y(p.value)); }); ctx.stroke();
  };
  line(result.previous || [], '#6e7681', [], 1.4);
  line(result.aligned, '#56d4dd', [4, 4], 1.3);
  line(result.actual, '#e6edf3', [], 2);
  if (result.bottom?.twoRedSixMonthCandles) {
    for (const range of (result.bottom.redSixMonthRanges || [])) {
      line((result.previous || []).filter(p => p.time >= range.start && p.time <= range.end), '#f85149', [], 2.2);
      line(result.actual.filter(p => p.time >= range.start && p.time <= range.end), '#f85149', [], 2.2);
    }
  }
  line(result.nextCycle || [], '#e3b341', [7, 4], 2.2);
  line(result.nextCycleRecent || [], '#f0883e', [3, 5], 2.1);
  // Once the full next-cycle projection exists, do not overlay the older
  // speed scenarios: their earlier peaks obscure the selected ATH timing.
  if (!(result.nextCycle || []).length) {
    for (const scenario of result.scenarios) line(scenario.points, scenario.name === 'Ajusté' ? '#e3b341' : scenario.name === 'Ralenti' ? '#58a6ff' : '#db61a2', [5, 5]);
  }
  ctx.setLineDash([3, 5]); ctx.strokeStyle = '#8b949e'; ctx.lineWidth = 1;
  ctx.beginPath(); ctx.moveTo(x(result.asOf), pad.top); ctx.lineTo(x(result.asOf), bottom); ctx.stroke();
  ctx.setLineDash([]);
  for (const event of (result.events || [])) {
    const p = event.point;
    if (p.time < start || p.time > end || !Number.isFinite(p.value)) continue;
    const color = event.kind === 'ath' ? '#f2cc60' : '#7ee787';
    ctx.beginPath(); ctx.fillStyle = color; ctx.arc(x(p.time), y(p.value), 5, 0, Math.PI * 2); ctx.fill();
    ctx.font = '11px monospace'; ctx.fillStyle = color; ctx.textAlign = 'center';
    const labelY = event.kind === 'bottom' ? Math.min(bottom - 4, y(p.value) + 20) : Math.max(14, y(p.value) - 10);
    ctx.fillText(event.label, x(p.time), labelY);
  }
  ctx.restore();
  ctx.fillStyle = '#8b949e'; ctx.textAlign = 'right'; ctx.fillText('Observation au ' + btcDate(result.asOf), Math.min(right, Math.max(190, x(result.asOf))), 16);
  if (btcCycleHover) drawBTCCycleHover(ctx, btcCycleHover);
}

function drawBTCCycleHover(ctx, hover) {
  const f = btcCycleFrame;
  if (!f) return;
  const t = f.start + (hover.x - f.pad.left) / (f.right - f.pad.left) * (f.end - f.start);
  const clampedY = Math.max(f.pad.top, Math.min(f.bottom, hover.y));
  const logValue = f.high - (clampedY - f.pad.top) / (f.bottom - f.pad.top) * (f.high - f.low);
  const value = Math.exp(logValue);
  const future = t > btcCycleResult.asOf;
  const lines = [[future ? 'Scénarios' : 'Observé', btcDate(t)]];
  const sets = future
    ? ((btcCycleResult.nextCycle || []).length ? [['Cycle 2018–2022', btcCycleResult.nextCycle], ['Cycle 2022–2026', btcCycleResult.nextCycleRecent || []]] : btcCycleResult.scenarios.map(s => [s.name, s.points]))
    : [['BTC', btcCycleResult.actual], ['Recalé', btcCycleResult.aligned]];
  for (const [name, points] of sets) {
    if (t < points[0].time || t > points[points.length - 1].time) continue;
    const point = points.reduce((a, b) => Math.abs(a.time - t) < Math.abs(b.time - t) ? a : b);
    lines.push([name, btcMoney(point.value)]);
  }
  ctx.save(); ctx.setLineDash([4, 4]); ctx.strokeStyle = '#6e7681'; ctx.lineWidth = 1;
  ctx.beginPath(); ctx.moveTo(hover.x, f.pad.top); ctx.lineTo(hover.x, f.bottom); ctx.stroke();
  ctx.beginPath(); ctx.moveTo(f.pad.left, clampedY); ctx.lineTo(f.right, clampedY); ctx.stroke();
  ctx.setLineDash([]);
  const priceLabel = btcMoney(value);
  ctx.font = '11px monospace';
  const labelWidth = ctx.measureText(priceLabel).width + 12;
  const labelLeft = f.right + 4;
  ctx.fillStyle = '#30363d';
  ctx.fillRect(labelLeft, clampedY - 10, labelWidth, 20);
  ctx.fillStyle = '#e6edf3';
  ctx.textAlign = 'left';
  ctx.fillText(priceLabel, labelLeft + 6, clampedY + 4);
  const width = Math.min(230, f.w - 24), height = lines.length * 20 + 14;
  const left = Math.max(8, Math.min(f.w - width - 8, hover.x + 12));
  const top = Math.max(f.pad.top, Math.min(f.bottom - height, hover.y));
  ctx.fillStyle = '#161b22'; ctx.fillRect(left, top, width, height);
  ctx.font = '11px monospace'; ctx.fillStyle = '#e6edf3';
  lines.forEach(([name, value], i) => { ctx.textAlign = 'left'; ctx.fillText(name, left + 8, top + 20 + i * 20); ctx.textAlign = 'right'; ctx.fillText(value, left + width - 8, top + 20 + i * 20); });
  ctx.restore();
}

document.getElementById('charts-canvas').addEventListener('pointermove', event => {
  if (!chartCycleMode || !btcCycleFrame) return;
  const rect = event.currentTarget.getBoundingClientRect();
  const x = event.clientX - rect.left, y = event.clientY - rect.top, f = btcCycleFrame;
  btcCycleHover = x >= f.pad.left && x <= f.right && y >= f.pad.top && y <= f.bottom ? { x, y } : null;
  drawBTCCycle();
});
document.getElementById('charts-canvas').addEventListener('pointerleave', () => { if (chartCycleMode) { btcCycleHover = null; drawBTCCycle(); } });
window.addEventListener('resize', () => { if (chartCycleMode) drawBTCCycle(); });
