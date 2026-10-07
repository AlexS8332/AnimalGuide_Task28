'use strict';

/* Окно «Локальная модель» (v26): сервер с OpenAI-совместимым API на своей
   машине или в своей сети (Ollama, LM Studio, llama.cpp).
   Подключается после app.js и пользуется его помощниками (app, esc,
   actions, factsAPI, toast).

   Блок «Сервер» — адрес, вид и версия сервера, модели (параметры,
   квантование, размер, умеет ли инструменты) и что сейчас в памяти.
   «Проверки» — запросы от простого к сложному; у каждого — промпт,
   ожидание, ответ модели, вердикт кода, время, токены и скорость.
   «Вопрос» — свободный запрос к локальной модели.

   Ответы модели — данные, только через esc(). Стабильные id и классы для
   сценариев: #local-button, #local-root, #local-server[data-ok],
   .local-model[data-name], #local-checks, .local-check[data-id],
   .local-run[data-id], #local-run-all, .local-result[data-id][data-ok],
   #local-summary, #local-q, #local-ask, #local-answer. */

const loc = {
  status: null,       // {ok, code, data, error} — GET /api/local/status
  results: {},        // id проверки → Result
  running: '',        // id проверки, которая идёт сейчас
  queue: [],          // что ещё прогнать в «Все по очереди»
  q: '',
  asking: false,
  answer: null,       // Result свободного вопроса
};
app.local = loc;

function locSec(ms) { return typeof ms === 'number' ? (ms / 1000).toFixed(1) + ' с' : '—'; }
function locGB(b) { return b > 0 ? (b / 1073741824).toFixed(1) + ' ГБ' : ''; }

async function locLoad() {
  loc.status = await factsAPI('GET', '/api/local/status');
  locRenderButton();
}

function locRenderButton() {
  let btn = $('local-button');
  if (!btn) {
    const anchor = $('windows-button');
    if (!anchor) return;
    btn = document.createElement('button');
    btn.type = 'button';
    btn.id = 'local-button';
    btn.className = 'ghost kb-button';
    btn.dataset.action = 'openWindow';
    btn.dataset.arg = 'local';
    anchor.parentElement.appendChild(btn);
  }
  const r = loc.status;
  let cls = '', tip = 'Локальная модель: сервер, модели, проверки';
  if (r && r.ok) {
    const d = r.data;
    cls = d.ok ? (d.modelFound ? 'ok' : 'warn') : 'bad';
    tip += '\n' + (d.ok ? `${d.config.model} @ ${d.config.baseUrl}` : 'сервер не отвечает: ' + (d.error || ''));
  }
  btn.innerHTML = `<span class="kb-dot ${cls}"></span>Локальная модель`;
  btn.title = tip;
}

app.windows.local = {
  title: 'Локальная модель',
  async render() {
    await locLoad();
    return `<div id="local-root" class="local">${locBodyHTML()}</div>`;
  },
};

function locPaint() {
  const root = $('local-root');
  if (root) root.innerHTML = locBodyHTML();
}

function locBodyHTML() {
  const r = loc.status;
  if (!r) return '<p class="hint">загружаю…</p>';
  if (!r.ok) return `<div class="facts-down"><b>Окно недоступно.</b><div>${esc(r.error)}</div></div>`;
  return locServerHTML(r.data) + locChecksHTML(r.data) + locAskHTML();
}

function locServerHTML(d) {
  const c = d.config || {};
  const kind = { ollama: 'Ollama', openai: 'OpenAI-совместимый' }[d.kind] || '';
  const head = d.ok
    ? `<span class="chip ok">отвечает</span> ${esc(kind)} ${esc(d.version || '')} · ${d.tookMs} мс`
    : `<span class="chip bad">не отвечает</span> ${esc(d.error || '')}`;
  const rows = (d.models || []).map(m => {
    const tags = [m.params, m.quant, m.format, locGB(m.size)].filter(Boolean).join(' · ');
    const caps = [m.tools ? 'инструменты' : '', m.thinking ? 'рассуждение' : '', m.embed ? 'эмбеддинги' : ''].filter(Boolean).join(', ');
    const loaded = (d.loaded || []).find(l => l.name === m.name);
    const mine = m.name === c.model;
    return `<tr class="local-model${mine ? ' mine' : ''}" data-name="${esc(m.name)}"><td>${mine ? '▶ ' : ''}<b>${esc(m.name)}</b></td>
      <td>${esc(tags)}</td><td>${esc(caps)}</td>
      <td>${loaded ? `<span class="chip ok">в памяти · ${esc(locGB(loaded.vram))} · контекст ${loaded.context || '—'}</span>` : ''}</td></tr>`;
  }).join('');
  return `<section class="local-sec" id="local-server" data-ok="${d.ok ? 1 : 0}">
    <h3>Сервер</h3>
    <div class="local-line"><code>${esc(c.baseUrl || '')}</code> ${head}</div>
    <div class="local-line hint">модель приложения: <b>${esc(c.model || '')}</b>${d.modelFound ? '' : ' — <b>нет на сервере</b>'} ·
      reasoning_effort=${esc(c.reasoning || '')} · API: <code>${esc((c.baseUrl || '') + '/v1/chat/completions')}</code></div>
    ${d.hint ? `<div class="local-line hint">${esc(d.hint)}</div>` : ''}
    ${rows ? `<table class="grid local-models"><tr><th>модель</th><th>вес</th><th>умеет</th><th></th></tr>${rows}</table>` : ''}
  </section>`;
}

function locChecksHTML(d) {
  const checks = d.checks || [];
  const busy = !!loc.running;
  const done = checks.map(c => loc.results[c.id]).filter(Boolean);
  let summary = '';
  if (done.length) {
    const ok = done.filter(r => r.ok === true).length;
    const took = done.reduce((s, r) => s + (r.tookMs || 0), 0);
    const speed = done.filter(r => r.tokPerS > 0);
    const tps = speed.length ? speed.reduce((s, r) => s + r.tokPerS, 0) / speed.length : 0;
    summary = `<div class="local-summary" id="local-summary"><b>${ok} из ${done.length}</b> прошли проверку кодом ·
      всего ${locSec(took)} · в среднем ${tps.toFixed(1)} ток/с</div>`;
  }
  const items = checks.map((c, i) => {
    const r = loc.results[c.id];
    const run = loc.running === c.id;
    return `<div class="local-check" data-id="${esc(c.id)}">
      <div class="local-check-head"><span class="chip">${i + 1} · ${esc(c.level)}</span> <b>${esc(c.title)}</b>
        <span class="hint">${esc(c.what)}</span>
        <button type="button" class="small local-run" data-id="${esc(c.id)}" data-action="localRun" data-arg="${esc(c.id)}"${busy ? ' disabled' : ''}>${run ? '<span class="thinking">думает</span>' : 'Запустить'}</button></div>
      <details class="local-prompt"><summary>промпт${c.tools ? ' + инструмент kb_search' : ''} · ожидается: ${esc(c.expect)}</summary>
        ${c.system ? `<pre class="local-sys">${esc(c.system)}</pre>` : ''}<pre>${esc(c.prompt)}</pre></details>
      ${r ? locResultHTML(r, c.id) : ''}
    </div>`;
  }).join('');
  return `<section class="local-sec" id="local-checks">
    <h3>Проверки: от простого к сложному
      <button type="button" class="solid small" id="local-run-all" data-action="localRunAll"${busy || !d.ok ? ' disabled' : ''}>Все по очереди</button></h3>
    ${summary}${items}
  </section>`;
}

function locResultHTML(r, id) {
  const state = r.error ? 'err' : r.ok === true ? '1' : r.ok === false ? '0' : '';
  const badge = r.error ? '<span class="chip bad">ошибка</span>' : r.ok === true ? '<span class="chip ok">ОК</span>' :
    r.ok === false ? '<span class="chip bad">не прошёл</span>' : '';
  const calls = (r.calls || []).map(c => `<div class="local-call">→ ${esc(c)}</div>`).join('');
  const u = r.usage || {};
  return `<div class="local-result" data-id="${esc(id || '')}" data-ok="${state}">
    <div class="local-answer-text">${r.error ? esc(r.error) : esc(r.answer || '')}${calls}</div>
    <div class="local-metrics">${badge} ${esc(r.verdict || '')}
      <span class="hint">· ${locSec(r.tookMs)} · ${u.prompt || 0} → ${u.completion || 0} ток. · ${(r.tokPerS || 0).toFixed(1)} ток/с · ${esc(r.model || '')}</span></div>
  </div>`;
}

function locAskHTML() {
  return `<section class="local-sec">
    <h3>Вопрос локальной модели</h3>
    <div class="local-ask-row">
      <textarea id="local-q" rows="2" data-change="localQ" placeholder="Например: чем питается рысь? Ответ — из памяти модели, без базы и источников.">${esc(loc.q)}</textarea>
      <button type="button" class="solid" id="local-ask" data-action="localAsk"${loc.asking ? ' disabled' : ''}>${loc.asking ? '<span class="thinking">думает</span>' : 'Спросить'}</button>
    </div>
    <div id="local-answer">${loc.answer ? locResultHTML(loc.answer, 'ask') : ''}</div>
  </section>`;
}

async function locRun(id) {
  loc.running = id;
  delete loc.results[id];
  locPaint();
  const r = await factsAPI('POST', '/api/local/run', { id });
  loc.results[id] = r.ok ? r.data : { error: r.error, tookMs: 0 };
  loc.running = '';
  locPaint();
}

Object.assign(actions, {
  async localRun(id) { if (!loc.running) await locRun(id); },
  async localRunAll() {
    if (loc.running || !loc.status || !loc.status.ok) return;
    loc.results = {};
    for (const c of loc.status.data.checks || []) await locRun(c.id);
  },
  localQ(v) { loc.q = v; },
  async localAsk() {
    const ta = $('local-q');
    if (ta) loc.q = ta.value;
    const prompt = loc.q.trim();
    if (!prompt || loc.asking) return;
    loc.asking = true;
    loc.answer = null;
    locPaint();
    const r = await factsAPI('POST', '/api/local/ask', { prompt });
    loc.answer = r.ok ? r.data : { error: r.error };
    loc.asking = false;
    locPaint();
  },
});

locRenderButton();
locLoad();
