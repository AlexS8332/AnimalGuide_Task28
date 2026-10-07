'use strict';

/* v28: вкладка «Локально и облако» окна «База знаний» и кнопка «Сеть».
   Подключается после kb.js: добавляет вкладку в kbTabs и отдаёт ей
   разметку через app.kbCompareHTML / app.kbCompareLoad.

   Вкладка: один вопрос (контрольный или свой) — всем сторонам сразу. Поиск
   общий и локальный, генератор у сторон свой; рядом ответы, источники,
   время генерации и поиска, токены в секунду, цена и вердикт правила
   (для контрольного вопроса). Ниже — сводка последнего отчёта kb compare:
   качество, скорость, стабильность.

   «Сеть» — сколько запросов процесс сделал на эту машину, в локальную сеть
   и в интернет; окно — таблица адресов.

   Стабильные id и классы: #cmp-root, #cmp-sides, #cmp-qid, #cmp-q,
   #cmp-go, .cmp-side[data-side][data-verdict], .cmp-answer, .cmp-src,
   .cmp-metrics, #cmp-report, .cmp-sum, #cmp-conclusion, #net-button,
   #net-root, .net-host[data-class], #net-internet, #net-reset. */

const cmp = {
  status: null,   // {ok, code, data, error} — GET /api/compare/status
  report: null,   // GET /api/compare/report
  form: { qid: '', q: '' },
  asking: false,
  answer: null,   // {ok, data, error} — POST /api/compare/ask
};
app.cmp = cmp;

kbTabs.push(['compare', 'Локально и облако', 'один вопрос локальной и облачной модели рядом; отчёт kb compare: качество, скорость, стабильность']);

const cmpVerdict = { correct: ['верно', 'ok'], partial: ['частично', 'warn'], wrong: ['неверно', 'bad'], abstain: ['не знаю', ''] };
const cmpSideRu = { local: 'локально', cloud: 'облако' };
function cmpSec(ms) { return typeof ms === 'number' ? (ms / 1000).toFixed(1) + ' с' : '—'; }
function cmpPct(f) { return typeof f === 'number' ? Math.round(100 * f) + ' %' : '—'; }

app.kbCompareLoad = async function () {
  cmp.status = await factsAPI('GET', '/api/compare/status');
  if (cmp.status.ok && cmp.status.data.report && !cmp.report) cmp.report = await factsAPI('GET', '/api/compare/report');
  if (!net.snap) await netLoad();
};

app.kbCompareHTML = function () {
  const st = cmp.status;
  if (!st) return '<p class="hint">загружаю…</p>';
  if (!st.ok) return `<div class="facts-down"><b>Сравнение недоступно.</b><div>${esc(st.error)}</div></div>`;
  const d = st.data;
  const sides = (d.sides || []).map(s => `<span class="chip ${s.name === 'local' ? 'ok' : ''}" data-side="${esc(s.name)}">${esc(cmpSideRu[s.name] || s.name)}: ${esc(s.label)}</span>`).join(' ');
  const one = (d.sides || []).length < 2 ? '<p class="hint">Облачной стороны нет: приложение запущено без <code>-compare cloud</code>. Вопрос уйдёт только локальной модели.</p>' : '';
  const n = net.snap && net.snap.ok ? net.snap.data : null;
  const netLine = n ? `<span class="chip ${n.internet ? 'warn' : 'ok'}" id="cmp-net">сеть процесса: эта машина ${n.byClass.loopback} · локальная сеть ${n.byClass.lan} · интернет ${n.internet}</span>` : '';
  return `<div id="cmp-root" class="cmp">
    <div class="cmp-head" id="cmp-sides">${sides}</div>
    <p class="hint">Поиск общий и локальный: база недели 6 (<code>kb.db</code>), эмбеддер, фильтр и переписывание — на этой машине. Различается только генератор ответа. Режим — <code>${esc(d.mode)}</code>: ответ с источниками и цитатами или «не знаю». ${netLine}</p>
    ${one}
    ${cmpFormHTML(d)}
    <div id="cmp-out">${cmpOutHTML()}</div>
    ${cmpReportHTML()}
  </div>`;
};

function cmpFormHTML(d) {
  const opts = (d.questions || []).map(q => `<option value="${esc(q.id)}"${q.id === cmp.form.qid ? ' selected' : ''}>${esc(q.id)} · ${esc(q.split)}${q.answerable ? '' : ' · вне базы'} — ${esc(kbCut(q.q, 80))}</option>`).join('');
  return `<div class="cmp-form">
    <select id="cmp-qid" data-change="cmpPick"><option value="">— свой вопрос —</option>${opts}</select>
    <input id="cmp-q" type="text" value="${esc(cmp.form.q)}" placeholder="Например: сколько весит манул?" data-change="cmpQ">
    <button type="button" class="solid" id="cmp-go" data-action="cmpAsk"${cmp.asking ? ' disabled' : ''}>${cmp.asking ? '<span class="thinking">отвечают</span>' : 'Спросить обе стороны'}</button>
  </div>`;
}

function cmpOutHTML() {
  const r = cmp.answer;
  if (!r) return '';
  if (!r.ok) return `<div class="facts-down">${esc(r.error)}</div>`;
  const cols = (r.data.results || []).map(x => {
    const v = cmpVerdict[x.verdict];
    const badge = v ? `<span class="chip ${v[1]}" title="${esc(x.why || '')}">правило: ${v[0]}</span>` : '';
    const srcs = (x.sources || []).map((s, i) => `<span class="chip cmp-src" data-chunk="${esc(s.chunk_id)}" title="${esc(s.chunk_id)}">[${i + 1}] ${esc(s.title || s.chunk_id)}${s.section ? ' › ' + esc(s.section) : ''}</span>`).join(' ');
    const body = x.error ? `<div class="facts-down">${esc(x.error)}</div>` :
      `<div class="cmp-answer md">${md(x.text)}</div>${srcs ? `<div class="chips">${srcs}</div>` : (x.unknown ? '<div class="hint">«не знаю» — источников нет</div>' : '')}`;
    return `<div class="cmp-side" data-side="${esc(x.side)}" data-verdict="${esc(x.verdict || '')}" data-error="${x.error ? 1 : 0}">
      <h4>${esc(cmpSideRu[x.side] || x.side)} <span class="hint">${esc(x.label)}</span> ${badge}</h4>
      ${body}
      <div class="cmp-metrics hint">генерация ${cmpSec(x.genMs)} · поиск ${Math.round(x.searchMs || 0)} мс · ${x.prompt} → ${x.output} ток. · ${(x.tokPerS || 0).toFixed(1)} ток/с · $${(x.costUsd || 0).toFixed(4)}</div>
    </div>`;
  }).join('');
  return `<div class="cmp-cols">${cols}</div><p class="hint">Обе стороны отвечали параллельно, всего ${cmpSec(r.data.tookMs)}.</p>`;
}

function cmpReportHTML() {
  const r = cmp.report;
  if (!r) return '<div id="cmp-report"><p class="hint">Отчёта сравнения ещё нет: <code>go run ./cmd/kb compare</code>.</p></div>';
  if (!r.ok) return `<div id="cmp-report"><p class="hint">${esc(r.error)}</p></div>`;
  const d = r.data, sides = d.sides || [];
  const head = sides.map(s => `<th>${esc(cmpSideRu[s.name] || s.name)}</th>`).join('');
  const row = (title, f) => `<tr><td>${esc(title)}</td>${sides.map(s => `<td>${esc(f(s))}</td>`).join('')}</tr>`;
  const st = s => s.stats || {}, sp = s => s.speed || {}, sb = s => s.stability || {};
  return `<div id="cmp-report">
    <h3>Отчёт kb compare · ${esc(new Date(d.created).toLocaleString('ru-RU'))} · ${esc((d.splits || []).join(' + '))}, повторов ${d.repeats} · судья — ${esc(d.judge)}</h3>
    <ul id="cmp-conclusion">${(d.conclusion || []).map(l => `<li>${esc(l)}</li>`).join('')}</ul>
    <table class="grid cmp-sum"><tr><th></th>${head}</tr>
      <tr class="cmp-group"><td colspan="${sides.length + 1}">качество</td></tr>
      ${row('верно / частично / неверно / не знаю', s => `${st(s).correct} / ${st(s).partial} / ${st(s).wrong} / ${st(s).abstain}`)}
      ${row('уверенные ошибки', s => st(s).confident_wrong)}
      ${row('«не знаю» вне базы', s => st(s).right_abstain)}
      <tr class="cmp-group"><td colspan="${sides.length + 1}">скорость</td></tr>
      ${row('генерация: среднее / медиана / p95', s => `${cmpSec(sp(s).gen_mean_ms)} / ${cmpSec(sp(s).gen_p50_ms)} / ${cmpSec(sp(s).gen_p95_ms)}`)}
      ${row('поиск (локальный)', s => Math.round(sp(s).search_mean_ms || 0) + ' мс')}
      ${row('токенов ответа в секунду', s => (sp(s).tok_per_s || 0).toFixed(1))}
      ${row('цена генерации', s => '$' + (sp(s).cost_usd || 0).toFixed(4))}
      <tr class="cmp-group"><td colspan="${sides.length + 1}">стабильность</td></tr>
      ${row('ошибок хода', s => sb(s).errors)}
      ${row('не прошли проверку цитат', s => sb(s).unverified)}
      ${row('смен вердикта между повторами', s => `${sb(s).flips} (${cmpPct(sb(s).flip_rate)})`)}
      ${row('те же источники во всех повторах', s => cmpPct(sb(s).same_sources))}
    </table>
  </div>`;
}

function cmpPaint() {
  if (kb.tab !== 'compare' || !$('cmp-root')) return;
  $('kb-body').innerHTML = app.kbCompareHTML();
}

Object.assign(actions, {
  cmpPick(v) {
    cmp.form.qid = v;
    const q = ((cmp.status && cmp.status.data.questions) || []).find(x => x.id === v);
    cmp.form.q = q ? q.q : '';
    cmpPaint();
  },
  cmpQ(v) { cmp.form.q = v; cmp.form.qid = ''; },
  async cmpAsk() {
    const inp = $('cmp-q');
    if (inp && inp.value.trim() !== cmp.form.q) { cmp.form.q = inp.value.trim(); cmp.form.qid = ''; }
    if (!cmp.form.q || cmp.asking) return;
    cmp.asking = true;
    cmp.answer = null;
    cmpPaint();
    cmp.answer = await factsAPI('POST', '/api/compare/ask', { q: cmp.form.q, qid: cmp.form.qid });
    cmp.asking = false;
    await netLoad();
    cmpPaint();
  },
});

/* ---------- «Сеть» ---------- */

const net = { snap: null, timer: null };
app.net = net;
const netClassRu = { loopback: 'эта машина', lan: 'локальная сеть', internet: 'интернет' };

async function netLoad() {
  net.snap = await factsAPI('GET', '/api/net');
  netRenderButton();
}

function netRenderButton() {
  let btn = $('net-button');
  if (!btn) {
    const anchor = $('windows-button');
    if (!anchor) return;
    btn = document.createElement('button');
    btn.type = 'button';
    btn.id = 'net-button';
    btn.className = 'ghost kb-button';
    btn.dataset.action = 'openWindow';
    btn.dataset.arg = 'net';
    anchor.parentElement.appendChild(btn);
  }
  const s = net.snap;
  if (!s || !s.ok) { btn.innerHTML = '<span class="kb-dot"></span>Сеть'; return; }
  const d = s.data;
  btn.innerHTML = `<span class="kb-dot ${d.internet ? 'warn' : 'ok'}"></span>Сеть: интернет <b id="net-internet">${d.internet}</b>`;
  btn.title = `Запросы процесса с ${new Date(d.since).toLocaleTimeString('ru-RU')}: эта машина ${d.byClass.loopback}, локальная сеть ${d.byClass.lan}, интернет ${d.internet}`;
}

app.windows.net = {
  title: 'Сеть процесса',
  async render() {
    await netLoad();
    return netHTML();
  },
};

function netHTML() {
  const s = net.snap;
  if (!s || !s.ok) return `<p class="hint">${esc(s ? s.error : 'загружаю…')}</p>`;
  const d = s.data;
  const rows = (d.hosts || []).map(h => `<tr class="net-host" data-class="${esc(h.class)}"><td><code>${esc(h.host)}</code></td><td>${esc(h.purpose || '')}</td>
    <td><span class="chip ${h.class === 'internet' ? 'warn' : 'ok'}">${esc(netClassRu[h.class] || h.class)}</span></td><td>${h.requests}</td><td>${h.errors || ''}</td></tr>`).join('');
  return `<div id="net-root">
    <p class="hint">Все HTTP-запросы процесса идут через один учитывающий транспорт. С ${esc(new Date(d.since).toLocaleTimeString('ru-RU'))}: эта машина — ${d.byClass.loopback}, локальная сеть — ${d.byClass.lan}, интернет — <b>${d.internet}</b>.</p>
    <table class="grid"><tr><th>адрес</th><th>что это</th><th>где</th><th>запросов</th><th>ошибок</th></tr>${rows || '<tr><td colspan="5" class="hint">запросов не было</td></tr>'}</table>
    <button type="button" class="small" id="net-reset" data-action="netReset">обнулить счётчики</button>
  </div>`;
}

Object.assign(actions, {
  async netReset() {
    await factsAPI('POST', '/api/net/reset');
    await netLoad();
    if ($('net-root')) $('window-body').innerHTML = netHTML();
  },
});

netRenderButton();
netLoad();
net.timer = setInterval(() => { if (document.visibilityState === 'visible') netLoad(); }, 4000);
