/* ==========================================================================
 * 樱隧 · 服务端控制台 —— 前端逻辑
 * 原生 JS，零依赖；所有接口走 /api/v2，HTTP Basic 认证
 * ========================================================================== */
'use strict';

(function () {

  /* ============================== 常量 ============================== */
  var AUTH_KEY = 'sakura_auth';
  var THEME_KEY = 'sakura_theme';
  var API_BASE = '/api/v2';
  var REQ_TIMEOUT = 8000;
  var POLL_INTERVAL = 5000;
  var RATE_CAP = 60;
  var GB = 1073741824;
  var DAY = 86400000;

  var PAGES = {
    overview: { title: '概览', sub: '实时监控' },
    proxies: { title: '隧道', sub: '全部隧道列表' },
    users: { title: '用户', sub: '账号与配额管理' },
    access: { title: '一键接入', sub: '生成安装命令' },
    settings: { title: '设置', sub: '服务端配置与外观' }
  };

  /* ============================== 状态 ============================== */
  var STATE = {
    auth: '',
    page: 'overview',
    sys: null,
    version: '',
    timer: null,
    polling: false,
    modalDone: null,
    lastSample: null,
    rate: { in: [], out: [], lastIn: 0, lastOut: 0 },
    recent: { items: [], loading: false },
    pickerItems: [],
    proxies: {
      page: 1, pageSize: 20, q: '', type: '', status: '',
      total: 0, items: [], loading: false, selected: new Set()
    },
    users: {
      page: 1, pageSize: 20, q: '',
      total: 0, items: [], loading: false
    }
  };

  /* ============================== 小工具 ============================== */
  function $(sel, root) { return (root || document).querySelector(sel); }
  function $$(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); }

  function esc(v) {
    return String(v === null || v === undefined ? '' : v)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  function escAttr(v) {
    return esc(v).replace(/\n/g, '&#10;').replace(/\r/g, '&#13;');
  }

  function debounce(fn, wait) {
    var t = null;
    return function () {
      var self = this, args = arguments;
      if (t) clearTimeout(t);
      t = setTimeout(function () { t = null; fn.apply(self, args); }, wait);
    };
  }

  function fmtNum(v) {
    var n = Number(v);
    if (!isFinite(n)) return '0';
    return n.toLocaleString('zh-CN');
  }

  function fmtBytes(v) {
    var n = Number(v) || 0;
    if (n <= 0) return '0 B';
    var units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
    var i = 0;
    while (n >= 1024 && i < units.length - 1) { n = n / 1024; i++; }
    return (i === 0 ? String(Math.round(n)) : n.toFixed(1)) + ' ' + units[i];
  }

  function fmtRate(v) { return fmtBytes(Math.max(0, Math.round(Number(v) || 0))) + '/s'; }

  function fmtUptime(sec) {
    var s = Math.max(0, Math.floor(Number(sec) || 0));
    if (s < 60) return '不足 1 分钟';
    var d = Math.floor(s / 86400);
    var h = Math.floor((s % 86400) / 3600);
    var m = Math.floor((s % 3600) / 60);
    if (d > 0) return d + ' 天 ' + h + ' 小时';
    if (h > 0) return h + ' 小时 ' + m + ' 分';
    return m + ' 分钟';
  }

  function toMs(v) {
    var n = Number(v) || 0;
    if (n <= 0) return 0;
    return n > 1e12 ? n : n * 1000;
  }

  function pad2(n) { return n < 10 ? '0' + n : String(n); }

  function fmtDate(v, emptyText) {
    var ms = toMs(v);
    if (!ms) return emptyText || '--';
    var d = new Date(ms);
    if (isNaN(d.getTime())) return emptyText || '--';
    return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate()) +
      ' ' + pad2(d.getHours()) + ':' + pad2(d.getMinutes());
  }

  function isoDate(v) {
    var ms = toMs(v);
    if (!ms) return '';
    var d = new Date(ms);
    if (isNaN(d.getTime())) return '';
    return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate());
  }

  function encodeBasic(cred) {
    var bytes = new TextEncoder().encode(cred);
    var bin = '';
    for (var i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin);
  }

  /* ============================== 提示与弹窗 ============================== */
  function toast(msg, type) {
    var wrap = $('#toastWrap');
    if (!wrap) return;
    var kind = type || 'info';
    var icons = { ok: 'ic-check', err: 'ic-warn', warn: 'ic-warn', info: 'ic-info' };
    var el = document.createElement('div');
    el.className = 'toast toast-' + kind;
    el.innerHTML = '<svg class="ic toast-ic" viewBox="0 0 24 24" aria-hidden="true"><use href="#' +
      (icons[kind] || 'ic-info') + '"/></svg><span class="toast-msg"></span>';
    $('.toast-msg', el).textContent = String(msg || '');
    wrap.appendChild(el);
    setTimeout(function () {
      el.classList.add('out');
      setTimeout(function () { if (el.parentNode) el.parentNode.removeChild(el); }, 320);
    }, 3200);
  }

  function openModal(title, bodyHTML, footHTML, opts) {
    opts = opts || {};
    var root = $('#modalRoot');
    STATE.modalDone = null;
    root.hidden = false;
    root.innerHTML =
      '<div class="modal' + (opts.wide ? ' modal-wide' : '') + '" role="dialog" aria-modal="true">' +
        '<div class="modal-head">' +
          '<h3 class="modal-title">' + esc(title) + '</h3>' +
          '<button class="modal-close" type="button" aria-label="关闭">' +
            '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><use href="#ic-close"/></svg>' +
          '</button>' +
        '</div>' +
        '<div class="modal-body">' + (bodyHTML || '') + '</div>' +
        (footHTML === null ? '' : '<div class="modal-foot">' + (footHTML || '') + '</div>') +
      '</div>';

    var close = function () {
      root.hidden = true;
      root.innerHTML = '';
      root.onmousedown = null;
      STATE.modalDone = null;
    };
    root.onmousedown = function (e) { if (e.target === root) close(); };
    $('.modal-close', root).addEventListener('click', close);

    return {
      root: root,
      close: close,
      q: function (s) { return $(s, root); },
      qa: function (s) { return $$(s, root); }
    };
  }

  function confirmDialog(title, messageHTML, opts) {
    opts = opts || {};
    return new Promise(function (resolve) {
      var m = openModal(
        title,
        '<p class="confirm-text">' + messageHTML + '</p>',
        '<button class="btn btn-ghost" type="button" data-act="cancel">取消</button>' +
        '<button class="btn ' + (opts.danger === false ? 'btn-primary' : 'btn-danger') + '" type="button" data-act="ok">' +
        esc(opts.okText || '确认删除') + '</button>'
      );
      var done = function (v) { m.close(); resolve(v); };
      STATE.modalDone = done;
      m.q('[data-act="cancel"]').addEventListener('click', function () { done(false); });
      m.q('[data-act="ok"]').addEventListener('click', function () { done(true); });
    });
  }

  /* ============================== 复制 ============================== */
  function copyText(text) {
    return new Promise(function (resolve) {
      var fallback = function () {
        try {
          var ta = document.createElement('textarea');
          ta.value = text;
          ta.setAttribute('readonly', 'readonly');
          ta.style.position = 'fixed';
          ta.style.top = '-1000px';
          ta.style.opacity = '0';
          document.body.appendChild(ta);
          ta.select();
          ta.setSelectionRange(0, ta.value.length);
          var ok = document.execCommand('copy');
          document.body.removeChild(ta);
          resolve(!!ok);
        } catch (e) {
          resolve(false);
        }
      };
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(text).then(function () { resolve(true); }, fallback);
      } else {
        fallback();
      }
    });
  }

  function doCopy(text, okMsg) {
    copyText(String(text || '')).then(function (ok) {
      if (ok) toast(okMsg || '已复制到剪贴板', 'ok');
      else toast('复制失败，请手动选中后复制', 'err');
    });
  }

  /* ============================== 接口层 ============================== */
  async function api(path, options) {
    options = options || {};
    var headers = Object.assign({ 'Accept': 'application/json' }, options.headers || {});
    if (STATE.auth) headers['Authorization'] = 'Basic ' + STATE.auth;

    var body;
    if (options.body !== undefined && options.body !== null) {
      headers['Content-Type'] = 'application/json';
      body = JSON.stringify(options.body);
    }

    var ctrl = new AbortController();
    var timer = setTimeout(function () { ctrl.abort(); }, REQ_TIMEOUT);
    var res;

    try {
      res = await fetch(API_BASE + path, {
        method: options.method || 'GET',
        headers: headers,
        body: body,
        cache: 'no-store',
        signal: ctrl.signal
      });
    } catch (e) {
      clearTimeout(timer);
      throw new Error(e && e.name === 'AbortError' ? '请求超时，请检查服务端是否在线' : '网络异常，无法连接服务端');
    }

    try {
      /* 服务端成功与失败均为信封：{code, msg, data}；失败时 HTTP 状态码 = code */
      var body = await res.json().catch(function () { return null; });
      if (res.status === 401) {
        if (options.noAuthReset) throw new Error('账号或密码错误');
        logout(true);
        throw new Error('登录凭据已失效，请重新登录');
      }
      if (!res.ok) {
        throw new Error((body && body.msg) ? body.msg : ('请求失败（HTTP ' + res.status + '）'));
      }
      var data = (body && body.data !== undefined) ? body.data : body;
      return data || {};
    } finally {
      clearTimeout(timer);
    }
  }

  /* ============================== 主题 ============================== */
  function applyTheme(theme, persist) {
    var t = theme === 'dark' ? 'dark' : 'light';
    document.documentElement.setAttribute('data-theme', t);
    var icon = $('#themeIcon');
    if (icon) icon.setAttribute('href', t === 'dark' ? '#ic-sun' : '#ic-moon');
    var sw = $('#themeSwitch');
    if (sw) sw.setAttribute('aria-checked', t === 'dark' ? 'true' : 'false');
    if (persist) { try { localStorage.setItem(THEME_KEY, t); } catch (e) {} }
  }

  function currentTheme() {
    return document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'light';
  }

  function toggleTheme() {
    var next = currentTheme() === 'dark' ? 'light' : 'dark';
    applyTheme(next, true);
    toast(next === 'dark' ? '已切换到夜樱深色模式' : '已切换到晨樱浅色模式', 'ok');
    if (STATE.page === 'overview') requestAnimationFrame(drawRateChart);
  }

  function initTheme() {
    var saved = null;
    try { saved = localStorage.getItem(THEME_KEY); } catch (e) {}
    var t = saved;
    if (!t) {
      t = (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) ? 'dark' : 'light';
    }
    applyTheme(t, false);
  }

  /* ============================== 飘落樱花 ============================== */
  function buildPetals() {
    var layer = $('#sakuraLayer');
    if (!layer) return;
    var reduce = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    if (reduce) return;

    var colors = ['#ff9ec4', '#ffd7e8', '#f7b8d4', '#e7c8f2', '#ffc3da'];
    var count = 26;
    var html = '';
    for (var i = 0; i < count; i++) {
      var size = 9 + Math.random() * 13;
      var left = Math.random() * 100;
      var dur = 11 + Math.random() * 13;
      var delay = -Math.random() * 26;
      var sway = 28 + Math.random() * 92;
      var op = 0.34 + Math.random() * 0.46;
      html += '<span class="petal" style="left:' + left.toFixed(2) + 'vw;width:' + size.toFixed(1) +
        'px;height:' + (size * 1.15).toFixed(1) + 'px;color:' + colors[i % colors.length] +
        ';--dur:' + dur.toFixed(2) + 's;--delay:' + delay.toFixed(2) + 's;--sway:' + sway.toFixed(0) +
        'px;--op:' + op.toFixed(2) + '"><svg viewBox="0 0 32 32" aria-hidden="true"><use href="#petal"/></svg></span>';
    }
    layer.innerHTML = html;
  }

  /* ============================== 空状态 / 骨架屏 ============================== */
  function emptyState(text, sub) {
    return '<div class="empty">' +
      '<svg class="mascot mascot-sm empty-art" viewBox="0 0 240 260" role="img" aria-label="看板娘"><use href="#mascot"/></svg>' +
      '<p class="empty-text">' + esc(text) + '</p>' +
      (sub ? '<p class="empty-sub">' + esc(sub) + '</p>' : '') +
      '</div>';
  }

  function tableSkeleton(colCount, rowCount) {
    var widths = ['62%', '42%', '54%', '78%', '38%', '50%', '58%', '44%', '52%', '70%'];
    var head = '';
    var c;
    for (c = 0; c < colCount; c++) head += '<th></th>';
    var rows = '';
    var r;
    for (r = 0; r < rowCount; r++) {
      var cells = '';
      for (c = 0; c < colCount; c++) {
        cells += '<td><div class="sk" style="width:' + widths[(c + r) % widths.length] + '"></div></td>';
      }
      rows += '<tr class="skeleton-row">' + cells + '</tr>';
    }
    return '<table class="table"><thead><tr>' + head + '</tr></thead><tbody>' + rows + '</tbody></table>';
  }

  function recentSkeleton(n) {
    var out = '';
    for (var i = 0; i < n; i++) {
      out += '<div class="recent-item">' +
        '<div class="sk" style="width:132px"></div>' +
        '<div class="sk" style="width:120px;margin-left:auto"></div>' +
        '</div>';
    }
    return out;
  }

  function spawnRipple(btn, ev) {
    var rect = btn.getBoundingClientRect();
    var size = Math.max(rect.width, rect.height);
    var span = document.createElement('span');
    span.className = 'ripple';
    span.style.width = size + 'px';
    span.style.height = size + 'px';
    span.style.left = (ev.clientX - rect.left - size / 2) + 'px';
    span.style.top = (ev.clientY - rect.top - size / 2) + 'px';
    btn.appendChild(span);
    setTimeout(function () { if (span.parentNode) span.parentNode.removeChild(span); }, 660);
  }

  /* ============================== 概览 ============================== */
  function renderOverview() {
    var grid = $('#statGrid');
    if (!grid) return;
    var sys = STATE.sys;
    if (!sys) {
      grid.innerHTML = recentSkeleton(1);
      return;
    }
    var st = sys.status || {};
    var proxyCount = 0;
    var pt = st.proxyTypeCount || {};
    Object.keys(pt).forEach(function (k) { proxyCount += Number(pt[k]) || 0; });

    var cards = [
      { label: '在线客户端', value: fmtNum(st.clientCounts), note: '当前在线的客户端数量', icon: 'ic-pulse', hero: true },
      { label: '隧道数', value: fmtNum(proxyCount), note: '按协议类型合计', icon: 'ic-tunnel' },
      { label: '用户数', value: fmtNum(st.userCount), note: '已创建的用户账号', icon: 'ic-user' },
      { label: '当前连接', value: fmtNum(st.curConns), note: '实时活跃连接数', icon: 'ic-server' },
      { label: '总上行', value: fmtBytes(st.totalTrafficIn), note: '服务端累计上行流量', icon: 'ic-up' },
      { label: '总下行', value: fmtBytes(st.totalTrafficOut), note: '服务端累计下行流量', icon: 'ic-down' },
      { label: '运行时长', value: fmtUptime(st.uptimeSec), note: '服务端启动至今', icon: 'ic-clock' }
    ];

    grid.innerHTML = cards.map(function (c) {
      return '<div class="stat' + (c.hero ? ' stat-hero' : '') + '">' +
        '<div class="stat-top">' +
          '<svg class="stat-ic ic" viewBox="0 0 24 24" aria-hidden="true"><use href="#' + c.icon + '"/></svg>' +
          '<span class="stat-label">' + esc(c.label) + '</span>' +
        '</div>' +
        '<div class="stat-value">' + esc(c.value) + '</div>' +
        '<div class="stat-note">' + esc(c.note) + '</div>' +
      '</div>';
    }).join('');
  }

  function renderRecent() {
    var box = $('#recentProxies');
    if (!box) return;
    if (STATE.recent.loading) { box.innerHTML = recentSkeleton(4); return; }
    var items = STATE.recent.items || [];
    if (!items.length) {
      box.innerHTML = emptyState('暂无数据', '还没有隧道接入，去「用户」页创建账号并发一份安装命令吧～');
      return;
    }
    box.innerHTML = items.slice(0, 5).map(function (it) {
      var info = proxySpecInfo(it.spec);
      var st = it.status || {};
      var online = String(st.phase || '').toLowerCase() === 'online';
      return '<div class="recent-item">' +
        '<span class="badge badge-type">' + esc(info.type.toUpperCase()) + '</span>' +
        '<div>' +
          '<div class="recent-name">' + esc(it.name) + '</div>' +
          '<div class="recent-meta">' + esc(it.user || '--') + ' · ' + esc(info.primary) + '</div>' +
        '</div>' +
        '<div class="recent-right">' +
          '<span>' + fmtBytes(st.todayTrafficIn) + '</span>' +
          '<span class="muted">/</span>' +
          '<span>' + fmtBytes(st.todayTrafficOut) + '</span>' +
          '<span class="status-cell"><i class="dot ' + (online ? 'on' : 'off') + '"></i>' +
          '<span class="status-text">' + (online ? '在线' : '离线') + '</span></span>' +
        '</div>' +
      '</div>';
    }).join('');
  }

  function pushRatePoint(vin, vout) {
    var a = STATE.rate.in;
    var b = STATE.rate.out;
    a.push(vin);
    b.push(vout);
    while (a.length > RATE_CAP) a.shift();
    while (b.length > RATE_CAP) b.shift();
    STATE.rate.lastIn = vin;
    STATE.rate.lastOut = vout;
    var el1 = $('#rateIn');
    var el2 = $('#rateOut');
    if (el1) el1.textContent = fmtRate(vin);
    if (el2) el2.textContent = fmtRate(vout);
  }

  function pushRate(st) {
    var now = Date.now();
    var vin = Number(st.totalTrafficIn) || 0;
    var vout = Number(st.totalTrafficOut) || 0;
    if (!STATE.lastSample) {
      STATE.rate.in.length = 0;
      STATE.rate.out.length = 0;
      pushRatePoint(0, 0);
    } else {
      var dt = (now - STATE.lastSample.t) / 1000;
      if (dt > 0.2) {
        var rin = Math.max(0, (vin - STATE.lastSample.in) / dt);
        var rout = Math.max(0, (vout - STATE.lastSample.out) / dt);
        pushRatePoint(rin, rout);
      }
    }
    STATE.lastSample = { t: now, in: vin, out: vout };
  }

  function drawRateChart() {
    var cv = $('#rateChart');
    if (!cv || !cv.parentElement) return;
    var wrapW = cv.parentElement.clientWidth || 640;
    var wrapH = cv.clientHeight || 172;
    var dpr = Math.min(window.devicePixelRatio || 1, 2);
    var needW = Math.round(wrapW * dpr);
    var needH = Math.round(wrapH * dpr);
    if (cv.width !== needW || cv.height !== needH) {
      cv.width = needW;
      cv.height = needH;
    }
    var ctx = cv.getContext('2d');
    if (!ctx) return;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, wrapW, wrapH);

    var dataIn = STATE.rate.in;
    var dataOut = STATE.rate.out;
    var max = 1024;
    var i;
    for (i = 0; i < dataIn.length; i++) max = Math.max(max, dataIn[i]);
    for (i = 0; i < dataOut.length; i++) max = Math.max(max, dataOut[i]);
    max = max * 1.16;

    var padL = 8;
    var padR = 8;
    var padT = 12;
    var padB = 18;
    var plotW = Math.max(10, wrapW - padL - padR);
    var plotH = Math.max(10, wrapH - padT - padB);

    var dark = currentTheme() === 'dark';
    var gridColor = dark ? 'rgba(255,215,232,.16)' : 'rgba(123,94,167,.16)';
    var textColor = dark ? 'rgba(255,215,232,.55)' : 'rgba(123,94,167,.55)';

    ctx.strokeStyle = gridColor;
    ctx.lineWidth = 1;
    ctx.setLineDash([4, 5]);
    var g;
    for (g = 0; g <= 3; g++) {
      var gy = padT + (plotH * g) / 3;
      ctx.beginPath();
      ctx.moveTo(padL, gy);
      ctx.lineTo(padL + plotW, gy);
      ctx.stroke();
    }
    ctx.setLineDash([]);

    /* 零值基线，避免空数据时图表显得残缺 */
    ctx.strokeStyle = dark ? 'rgba(255,215,232,.3)' : 'rgba(123,94,167,.22)';
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(padL, padT + plotH);
    ctx.lineTo(padL + plotW, padT + plotH);
    ctx.stroke();

    ctx.fillStyle = textColor;
    ctx.font = '10px "PingFang SC","Microsoft YaHei",system-ui,sans-serif';
    ctx.textBaseline = 'bottom';
    ctx.fillText(fmtRate(max), padL + 2, padT - 2);
    ctx.textBaseline = 'top';
    ctx.fillText('最近 60 次采样', padL + 2, padT + plotH + 4);

    drawSeries(ctx, dataIn, padL, padT, plotW, plotH, max, 'rgba(255,158,196,1)', 'rgba(255,158,196,.42)', 'rgba(255,158,196,0)', dark);
    drawSeries(ctx, dataOut, padL, padT, plotW, plotH, max, 'rgba(123,94,167,1)', 'rgba(123,94,167,.34)', 'rgba(123,94,167,0)', dark);

    if (dataIn.length < 3) {
      ctx.fillStyle = textColor;
      ctx.font = '12px "PingFang SC","Microsoft YaHei",system-ui,sans-serif';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.fillText('正在采集速率数据…', padL + plotW / 2, padT + plotH / 2);
      ctx.textAlign = 'left';
    }
  }

  function drawSeries(ctx, arr, padL, padT, plotW, plotH, max, stroke, fillTop, fillBottom, dark) {
    if (!arr || !arr.length) return;
    var stepX = plotW / (RATE_CAP - 1);
    var start = 0;
    var pts = [];
    var i;
    for (i = 0; i < arr.length; i++) {
      var x = padL + (start + i) * stepX;
      var ratio = Math.min(Math.max(arr[i], 0), max) / max;
      var y = padT + plotH - ratio * plotH;
      pts.push([x, y]);
    }
    if (pts.length === 1) {
      pts.push([pts[0][0] + stepX, pts[0][1]]);
    }

    var grad = ctx.createLinearGradient(0, padT, 0, padT + plotH);
    grad.addColorStop(0, fillTop);
    grad.addColorStop(1, fillBottom);

    ctx.beginPath();
    ctx.moveTo(pts[0][0], padT + plotH);
    for (i = 0; i < pts.length; i++) ctx.lineTo(pts[i][0], pts[i][1]);
    ctx.lineTo(pts[pts.length - 1][0], padT + plotH);
    ctx.closePath();
    ctx.fillStyle = grad;
    ctx.fill();

    ctx.beginPath();
    ctx.moveTo(pts[0][0], pts[0][1]);
    for (i = 1; i < pts.length; i++) ctx.lineTo(pts[i][0], pts[i][1]);
    ctx.strokeStyle = stroke;
    ctx.lineWidth = 2;
    ctx.lineJoin = 'round';
    ctx.lineCap = 'round';
    ctx.shadowColor = stroke;
    ctx.shadowBlur = dark ? 8 : 6;
    ctx.stroke();
    ctx.shadowBlur = 0;

    var last = pts[pts.length - 1];
    ctx.beginPath();
    ctx.arc(last[0], last[1], 3, 0, Math.PI * 2);
    ctx.fillStyle = '#fff';
    ctx.fill();
    ctx.lineWidth = 2;
    ctx.strokeStyle = stroke;
    ctx.stroke();
  }

  /* ============================== 隧道 ============================== */
  function proxySpecInfo(spec) {
    spec = spec || {};
    var type = String(spec.type || 'tcp').toLowerCase();
    var conf = spec[type] || {};
    var out = { type: type, primary: '—', extra: '' };

    if (type === 'tcp' || type === 'udp') {
      out.primary = conf.remotePort ? ('远程端口 ' + conf.remotePort) : '未指定远程端口';
    } else if (type === 'http' || type === 'https' || type === 'tcpmux') {
      var cd = conf.customDomains;
      if (Array.isArray(cd) && cd.length) out.primary = cd.join(' / ');
      else if (conf.subdomain) out.primary = '子域 ' + conf.subdomain;
      else out.primary = '未绑定域名';
    } else if (type === 'stcp' || type === 'sudp' || type === 'xtcp') {
      out.primary = conf.secretKey ? '需密钥访问' : '点对点穿透';
    } else {
      out.primary = '—';
    }

    var tr = conf.transport || {};
    var flags = [];
    if (tr.useEncryption) flags.push('加密');
    if (tr.useCompression) flags.push('压缩');
    if (tr.bandwidthLimit && String(tr.bandwidthLimit) !== '0') flags.push('限速 ' + tr.bandwidthLimit + ' KB/s');
    out.extra = flags.join(' · ');
    return out;
  }

  function loadProxies(opts) {
    opts = opts || {};
    var p = STATE.proxies;
    if (!opts.silent) {
      p.loading = true;
      renderProxies();
      renderProxyPager();
    }
    var params = new URLSearchParams();
    params.set('page', String(p.page));
    params.set('pageSize', String(p.pageSize));
    if (p.q) params.set('q', p.q);
    if (p.type) params.set('type', p.type);
    if (p.status) params.set('status', p.status);

    return api('/proxies?' + params.toString()).then(function (data) {
      p.total = Number(data.total) || 0;
      p.items = Array.isArray(data.items) ? data.items : [];
      var alive = new Set();
      p.items.forEach(function (it) { alive.add(it.name); });
      var nextSel = new Set();
      p.selected.forEach(function (n) { if (alive.has(n)) nextSel.add(n); });
      p.selected = nextSel;
      p.loading = false;
      if (STATE.page === 'proxies' || !opts.silent) {
        renderProxies();
        renderProxyPager();
        syncProxyDeleteBtn();
      }
      return data;
    }).catch(function (err) {
      p.loading = false;
      if (STATE.page === 'proxies' || !opts.silent) renderProxies();
      if (!opts.silent) toast(err.message, 'err');
      return null;
    });
  }

  function renderProxies() {
    var p = STATE.proxies;
    var wrap = $('#proxyTableWrap');
    if (!wrap) return;
    if (p.loading) {
      wrap.innerHTML = tableSkeleton(9, 8);
      return;
    }
    if (!p.items.length) {
      wrap.innerHTML = emptyState('暂无隧道', '当前筛选条件下没有隧道记录，换个条件试试？');
      return;
    }

    var allChecked = p.items.every(function (it) { return p.selected.has(it.name); });
    var rows = p.items.map(function (it) {
      var info = proxySpecInfo(it.spec);
      var st = it.status || {};
      var online = String(st.phase || '').toLowerCase() === 'online';
      var checked = p.selected.has(it.name) ? ' checked' : '';
      return '<tr>' +
        '<td><input class="check" type="checkbox" data-pick="' + escAttr(it.name) + '"' +
          checked + ' aria-label="选择隧道 ' + escAttr(it.name) + '"></td>' +
        '<td>' +
          '<div class="cell-name">' + esc(it.name) + '</div>' +
          '<div class="cell-sub">' + esc(info.primary) + (info.extra ? ' · ' + esc(info.extra) : '') + '</div>' +
        '</td>' +
        '<td><span class="badge badge-type">' + esc(info.type.toUpperCase()) + '</span></td>' +
        '<td class="nowrap">' + esc(it.user || '--') + '</td>' +
        '<td class="nowrap"><span class="status-cell"><i class="dot ' + (online ? 'on' : 'off') + '"></i>' +
          '<span class="status-text">' + (online ? '在线' : '离线') + '</span></span></td>' +
        '<td class="num">' + fmtBytes(st.todayTrafficIn) + '</td>' +
        '<td class="num">' + fmtBytes(st.todayTrafficOut) + '</td>' +
        '<td class="num">' + fmtNum(st.curConns) + '</td>' +
        '<td><div class="ops">' +
          '<button class="op-btn" type="button" data-copy="' + escAttr(it.name) + '">复制名称</button>' +
          '<button class="op-btn danger" type="button" data-del="' + escAttr(it.name) + '">删除</button>' +
        '</div></td>' +
      '</tr>';
    }).join('');

    wrap.innerHTML =
      '<table class="table"><thead><tr>' +
        '<th style="width:40px"><input class="check" type="checkbox" id="proxyAll"' + (allChecked ? ' checked' : '') + ' aria-label="全选"></th>' +
        '<th>名称</th><th>类型</th><th>用户</th><th>状态</th>' +
        '<th>今日上行</th><th>今日下行</th><th>当前连接</th><th>操作</th>' +
      '</tr></thead><tbody>' + rows + '</tbody></table>';
    syncProxyDeleteBtn();
  }

  function syncProxyDeleteBtn() {
    var btn = $('#proxyDelSel');
    if (!btn) return;
    var n = STATE.proxies.selected.size;
    btn.disabled = n === 0;
    btn.innerHTML = '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><use href="#ic-trash"/></svg>' +
      (n ? '删除所选（' + n + '）' : '删除所选');
  }

  function deleteProxies(names) {
    if (!names || !names.length) return;
    var label = names.length === 1
      ? ('隧道 <b>' + esc(names[0]) + '</b>')
      : ('选中的 <b>' + names.length + '</b> 条隧道');
    confirmDialog('确认删除',
      '确定要删除' + label + '吗？删除后该隧道立即断开，客户端配置需要同步移除。'
    ).then(function (ok) {
      if (!ok) return;
      api('/proxies', { method: 'DELETE', body: { names: names } }).then(function () {
        toast('隧道已删除', 'ok');
        STATE.proxies.selected = new Set();
        syncProxyDeleteBtn();
        loadProxies();
        loadRecent(true);
      }).catch(function (err) { toast(err.message, 'err'); });
    });
  }

  /* ============================== 用户 ============================== */
  function userState(u) {
    var now = Date.now();
    var exp = toMs(u.expireAt);
    var expired = exp > 0 && exp <= now;
    var soon = exp > 0 && !expired && (exp - now) <= 7 * DAY;
    var disabled = u.enabled === false;
    return { expired: expired, soon: soon, disabled: disabled };
  }

  function lastSeenText(v) {
    var ms = toMs(v);
    if (!ms) return '从未上线';
    var diff = Date.now() - ms;
    if (diff < 0) diff = 0;
    if (diff < 60000) return '刚刚活跃';
    if (diff < 3600000) return Math.floor(diff / 60000) + ' 分钟前活跃';
    if (diff < 86400000) return Math.floor(diff / 3600000) + ' 小时前活跃';
    if (diff < 7 * 86400000) return Math.floor(diff / 86400000) + ' 天前活跃';
    return '最近 ' + fmtDate(v);
  }

  function loadUsers(opts) {
    opts = opts || {};
    var u = STATE.users;
    if (!opts.silent) {
      u.loading = true;
      renderUsers();
      renderUserPager();
    }
    var params = new URLSearchParams();
    params.set('page', String(u.page));
    params.set('pageSize', String(u.pageSize));
    if (u.q) params.set('q', u.q);

    return api('/users?' + params.toString()).then(function (data) {
      u.total = Number(data.total) || 0;
      u.items = Array.isArray(data.items) ? data.items : [];
      u.loading = false;
      renderUsers();
      renderUserPager();
      return data;
    }).catch(function (err) {
      u.loading = false;
      renderUsers();
      if (!opts.silent) toast(err.message, 'err');
      return null;
    });
  }

  function renderUsers() {
    var u = STATE.users;
    var wrap = $('#userTableWrap');
    if (!wrap) return;
    if (u.loading) {
      wrap.innerHTML = tableSkeleton(10, 8);
      return;
    }
    if (!u.items.length) {
      wrap.innerHTML = emptyState('暂无用户', '点击右上角「新建用户」，为客户开通第一个账号');
      return;
    }

    var rows = u.items.map(function (it) {
      var st = userState(it);
      var trCls = (st.disabled || st.expired) ? ' class="row-off"' : (st.soon ? ' class="row-warn"' : '');
      var name = esc(it.user);
      var sub = it.note ? '<div class="cell-sub">' + esc(it.note) + '</div>' : '';
      var expText = Number(it.expireAt) ? fmtDate(it.expireAt) : '永久';
      if (st.expired) expText += ' <span class="badge badge-plain" style="color:var(--danger)">已到期</span>';
      else if (st.soon) expText += ' <span class="badge badge-plain" style="color:var(--warn)">即将到期</span>';
      if (st.disabled) expText += ' <span class="badge badge-plain" style="color:var(--danger)">已停用</span>';

      return '<tr' + trCls + '>' +
        '<td><div class="cell-name">' + name + '</div>' + sub + '</td>' +
        '<td class="nowrap">' +
          '<span class="status-cell"><i class="dot ' + (it.online ? 'on' : 'off') + '"></i>' +
          '<span class="status-text">' + (it.online ? '在线' : '离线') + '</span></span>' +
          '<div class="cell-sub">' + esc(lastSeenText(it.lastSeenAt)) + '</div>' +
        '</td>' +
        '<td class="num">' + fmtNum(it.clientCount) + '</td>' +
        '<td class="num">' + fmtNum(it.proxyCount) + '</td>' +
        '<td class="num">↑' + fmtBytes(it.todayTrafficIn) + '<br>↓' + fmtBytes(it.todayTrafficOut) + '</td>' +
        '<td class="num">↑' + fmtBytes(it.trafficIn) + '<br>↓' + fmtBytes(it.trafficOut) + '</td>' +
        '<td class="num">' + (Number(it.bandwidthLimitKbps) > 0 ? fmtNum(it.bandwidthLimitKbps) + ' Kbps' : '不限速') + '</td>' +
        '<td class="num">' + (Number(it.trafficQuotaBytes) > 0 ? fmtBytes(it.trafficQuotaBytes) : '不限量') + '</td>' +
        '<td class="nowrap">' + expText + '</td>' +
        '<td><div class="ops">' +
          '<button class="op-btn" type="button" data-u-edit="' + escAttr(it.user) + '">编辑</button>' +
          '<button class="op-btn" type="button" data-u-token="' + escAttr(it.user) + '">重置密钥</button>' +
          '<button class="op-btn" type="button" data-u-access="' + escAttr(it.user) + '">一键接入</button>' +
          '<button class="op-btn" type="button" data-u-reset="' + escAttr(it.user) + '">清空流量</button>' +
          '<button class="op-btn danger" type="button" data-u-del="' + escAttr(it.user) + '">删除</button>' +
        '</div></td>' +
      '</tr>';
    }).join('');

    wrap.innerHTML =
      '<table class="table"><thead><tr>' +
        '<th>用户</th><th>在线</th><th>客户端</th><th>隧道</th><th>今日流量</th>' +
        '<th>累计流量</th><th>限速</th><th>配额</th><th>到期</th><th>操作</th>' +
      '</tr></thead><tbody>' + rows + '</tbody></table>';
  }

  function pagerHTML(page, pageSize, total) {
    var pages = Math.max(1, Math.ceil(total / pageSize));
    return '<span class="pager-info">共 <b>' + fmtNum(total) + '</b> 条 · 第 <b>' + page + '</b> / ' + pages + ' 页</span>' +
      '<button class="btn btn-ghost btn-sm" type="button" data-page-go="' + (page - 1) + '"' + (page <= 1 ? ' disabled' : '') + '>上一页</button>' +
      '<button class="btn btn-ghost btn-sm" type="button" data-page-go="' + (page + 1) + '"' + (page >= pages ? ' disabled' : '') + '>下一页</button>';
  }

  function renderProxyPager() {
    var el = $('#proxyPager');
    var p = STATE.proxies;
    if (el) el.innerHTML = pagerHTML(p.page, p.pageSize, p.total);
  }

  function renderUserPager() {
    var el = $('#userPager');
    var u = STATE.users;
    if (el) el.innerHTML = pagerHTML(u.page, u.pageSize, u.total);
  }

  function findUser(name) {
    var hit = null;
    STATE.users.items.forEach(function (u) { if (u.user === name) hit = u; });
    if (!hit) STATE.pickerItems.forEach(function (u) { if (u.user === name) hit = u; });
    return hit;
  }

  function openUserForm(mode, user) {
    var editing = mode === 'edit';
    var u = user || {};
    var quotaGb = Number(u.trafficQuotaBytes) > 0 ? Number((Number(u.trafficQuotaBytes) / GB).toFixed(2)) : 0;
    var expireVal = isoDate(u.expireAt);

    var body =
      '<div class="form-grid">' +
        '<label class="field">' +
          '<span class="field-label">用户名</span>' +
          '<input id="fUser" class="input" type="text" spellcheck="false" placeholder="3-32 位字母、数字或下划线" value="' +
            escAttr(editing ? (u.user || '') : '') + '"' + (editing ? ' disabled' : '') + '>' +
          '<span class="field-error" id="fUserErr"></span>' +
        '</label>' +
        '<label class="field">' +
          '<span class="field-label">最大隧道数</span>' +
          '<input id="fMax" class="input" type="number" min="0" step="1" value="' +
            escAttr(u.maxProxies === undefined || u.maxProxies === null ? 10 : u.maxProxies) + '">' +
          '<span class="form-hint">允许该用户同时创建的隧道上限</span>' +
        '</label>' +
        '<label class="field">' +
          '<span class="field-label">限速（Kbps）</span>' +
          '<input id="fBw" class="input" type="number" min="0" step="1" value="' +
            escAttr(u.bandwidthLimitKbps === undefined || u.bandwidthLimitKbps === null ? 0 : u.bandwidthLimitKbps) + '">' +
          '<span class="form-hint">0 表示不限速</span>' +
        '</label>' +
        '<label class="field">' +
          '<span class="field-label">流量配额（GB）</span>' +
          '<input id="fQuota" class="input" type="number" min="0" step="0.5" value="' +
            escAttr(quotaGb || 0) + '">' +
          '<span class="form-hint">0 表示不限量</span>' +
        '</label>' +
        '<label class="field">' +
          '<span class="field-label">到期时间</span>' +
          '<input id="fExpire" class="input" type="date" value="' + escAttr(expireVal) + '">' +
          '<span class="form-hint">留空表示永久有效</span>' +
        '</label>' +
        '<label class="field form-row-span">' +
          '<span class="field-label">备注</span>' +
          '<textarea id="fNote" class="input" placeholder="选填，例如用途、联系人">' + esc(u.note || '') + '</textarea>' +
        '</label>' +
      '</div>';

    var m = openModal(
      editing ? ('编辑用户 · ' + (u.user || '')) : '新建用户',
      body,
      '<button class="btn btn-ghost" type="button" data-act="cancel">取消</button>' +
      '<button class="btn btn-primary" type="button" data-act="save">' + (editing ? '保存修改' : '创建并生成密钥') + '</button>',
      { wide: true }
    );

    m.q('[data-act="cancel"]').addEventListener('click', m.close);
    m.q('[data-act="save"]').addEventListener('click', function () { submitUserForm(m, editing, u); });

    var userInput = m.q('#fUser');
    if (userInput && !editing) setTimeout(function () { userInput.focus(); }, 60);
  }

  function submitUserForm(m, editing, user) {
    var errEl = m.q('#fUserErr');
    var nameEl = m.q('#fUser');
    var name = nameEl.value.trim();

    if (errEl) errEl.textContent = '';
    if (!editing) {
      if (!/^[A-Za-z0-9_]{3,32}$/.test(name)) {
        if (errEl) errEl.textContent = '用户名需为 3-32 位字母、数字或下划线';
        nameEl.focus();
        return;
      }
    }

    var maxProxies = Math.max(0, Math.floor(Number(m.q('#fMax').value) || 0));
    var bw = Math.max(0, Math.floor(Number(m.q('#fBw').value) || 0));
    var quotaGb = Math.max(0, Number(m.q('#fQuota').value) || 0);
    var quotaBytes = Math.round(quotaGb * GB);
    var expireRaw = m.q('#fExpire').value;
    var expireAt = 0;
    if (expireRaw) {
      var parts = expireRaw.split('-');
      var dt = new Date(Number(parts[0]), Number(parts[1]) - 1, Number(parts[2]), 23, 59, 59);
      if (!isNaN(dt.getTime())) expireAt = Math.floor(dt.getTime() / 1000);
    }
    var note = m.q('#fNote').value.trim();

    var payload = {
      maxProxies: maxProxies,
      bandwidthLimitKbps: bw,
      trafficQuotaBytes: quotaBytes,
      expireAt: expireAt,
      note: note
    };

    var saveBtn = m.q('[data-act="save"]');
    saveBtn.disabled = true;

    var req;
    if (editing) {
      req = api('/users/' + encodeURIComponent(user.user), { method: 'PUT', body: payload });
    } else {
      payload.user = name;
      req = api('/users', { method: 'POST', body: payload });
    }

    req.then(function (data) {
      m.close();
      if (editing) {
        toast('用户资料已更新', 'ok');
        loadUsers({ silent: true });
        refreshPicker();
      } else {
        toast('用户创建成功', 'ok');
        if (data && data.token) showToken(data.token, '新用户的客户端密钥');
        STATE.users.page = 1;
        loadUsers();
        refreshPicker();
      }
    }).catch(function (err) {
      saveBtn.disabled = false;
      if (errEl) errEl.textContent = err.message;
      toast(err.message, 'err');
    });
  }

  function showToken(token, title) {
    var m = openModal(
      title || '客户端密钥',
      '<div class="warn-note">' +
        '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><use href="#ic-warn"/></svg>' +
        '<span>该密钥只显示一次，关闭弹窗后无法再次查看。请立即复制并交给对方妥善保存。</span>' +
      '</div>' +
      '<div class="token-box" style="margin-top:12px">' +
        '<div class="token-value" id="tokenValue">' + esc(token) + '</div>' +
        '<button class="btn btn-primary btn-sm" type="button" id="tokenCopy">' +
          '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><use href="#ic-copy"/></svg>复制</button>' +
      '</div>',
      '<button class="btn btn-primary" type="button" id="tokenDone">我已保存</button>'
    );
    m.q('#tokenCopy').addEventListener('click', function () { doCopy(token, '密钥已复制到剪贴板'); });
    m.q('#tokenDone').addEventListener('click', m.close);
  }

  function resetToken(user) {
    confirmDialog('重置密钥',
      '确定要为用户 <b>' + esc(user) + '</b> 重置客户端密钥吗？<br>旧密钥将立即失效，已部署的客户端需要重新安装。',
      { okText: '确认重置' }
    ).then(function (ok) {
      if (!ok) return;
      api('/users/' + encodeURIComponent(user) + '/token', { method: 'POST' }).then(function (data) {
        if (data && data.token) showToken(data.token, '新的客户端密钥');
        else toast('密钥已重置', 'ok');
        loadUsers({ silent: true });
      }).catch(function (err) { toast(err.message, 'err'); });
    });
  }

  function resetTraffic(user) {
    confirmDialog('清空累计流量',
      '确定要清空用户 <b>' + esc(user) + '</b> 的累计流量统计吗？<br>今日流量与累计流量会重新从 0 开始计算，配额不会因此退还。',
      { okText: '确认清空', danger: false }
    ).then(function (ok) {
      if (!ok) return;
      api('/users/' + encodeURIComponent(user) + '/reset-traffic', { method: 'POST' }).then(function () {
        toast('累计流量已清空', 'ok');
        loadUsers({ silent: true });
      }).catch(function (err) { toast(err.message, 'err'); });
    });
  }

  function deleteUser(user) {
    confirmDialog('删除用户',
      '确定要删除用户 <b>' + esc(user) + '</b> 吗？<br>该用户名下的隧道与密钥会一并失效，操作不可撤销。'
    ).then(function (ok) {
      if (!ok) return;
      api('/users/' + encodeURIComponent(user), { method: 'DELETE' }).then(function () {
        toast('用户已删除', 'ok');
        loadUsers();
        refreshPicker();
        loadRecent(true);
      }).catch(function (err) { toast(err.message, 'err'); });
    });
  }

  /* ============================== 一键接入 ============================== */
  function refreshPicker() {
    STATE.pickerItems = [];
    if (STATE.page === 'access') ensureAccessUsers(true);
  }

  function ensureAccessUsers(force) {
    if (STATE.pickerItems.length && !force) {
      renderAccessPicker();
      return Promise.resolve();
    }
    return api('/users?page=1&pageSize=200').then(function (data) {
      STATE.pickerItems = Array.isArray(data.items) ? data.items : [];
      renderAccessPicker();
      return null;
    }).catch(function (err) {
      renderAccessPicker();
      toast(err.message, 'err');
      return null;
    });
  }

  function renderAccessPicker() {
    var sel = $('#accessUser');
    if (!sel) return;
    var prev = sel.value;
    if (!STATE.pickerItems.length) {
      sel.innerHTML = '<option value="">暂无用户，请先创建</option>';
      return;
    }
    sel.innerHTML = STATE.pickerItems.map(function (u) {
      var st = userState(u);
      var tag = st.disabled ? '（已停用）' : (st.expired ? '（已到期）' : '');
      return '<option value="' + escAttr(u.user) + '">' + esc(u.user) + esc(tag) + '</option>';
    }).join('');
    var exists = STATE.pickerItems.some(function (u) { return u.user === prev; });
    if (prev && exists) sel.value = prev;
  }

  function dlLabel(key) {
    var map = {
      linux_amd64: 'Linux amd64', linux_arm64: 'Linux arm64', linux_arm: 'Linux arm',
      windows_amd64: 'Windows amd64', windows_arm64: 'Windows arm64',
      darwin_amd64: 'macOS amd64', darwin_arm64: 'macOS arm64',
      freebsd_amd64: 'FreeBSD amd64'
    };
    return map[key] || String(key).replace(/_/g, ' / ');
  }

  function generateAccess() {
    var sel = $('#accessUser');
    if (!sel) return;
    var user = sel.value;
    var box = $('#accessResult');
    if (!user) {
      toast('请先选择用户', 'warn');
      return;
    }
    box.innerHTML = '<p class="muted">正在生成接入命令…</p>';
    api('/users/' + encodeURIComponent(user) + '/install').then(function (data) {
      renderAccess(data, user);
    }).catch(function (err) {
      box.innerHTML = emptyState('生成失败', err.message);
      toast(err.message, 'err');
    });
  }

  function renderAccess(data, user) {
    var box = $('#accessResult');
    if (!box) return;
    var cards = [
      { key: 'linux', title: 'Linux 服务器', desc: '在终端以 root 或具备 sudo 权限的账号执行，会自动下载并注册为系统服务。' },
      { key: 'windows', title: 'Windows 客户端', desc: '在 PowerShell 中执行，自动下载对应架构程序并完成注册与开机自启。' },
      { key: 'docker', title: 'Docker 容器', desc: '已安装 Docker 的主机直接执行，容器随宿主机启动。' }
    ];

    var grids = '';
    cards.forEach(function (c) {
      var cmd = data[c.key];
      if (!cmd) return;
      grids +=
        '<div class="code-card">' +
          '<div class="code-head">' +
            '<span class="code-title">' +
              '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><use href="#ic-terminal"/></svg>' + esc(c.title) +
            '</span>' +
            '<button class="btn btn-ghost btn-sm" type="button" data-copy="' + escAttr(cmd) + '">' +
              '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><use href="#ic-copy"/></svg>复制' +
            '</button>' +
          '</div>' +
          '<pre class="code-block">' + esc(cmd) + '</pre>' +
          '<p class="code-desc">' + esc(c.desc) + '</p>' +
        '</div>';
    });

    var dls = data.downloads || {};
    var dlKeys = Object.keys(dls);
    var dlHtml = '';
    if (dlKeys.length) {
      dlHtml =
        '<div class="card-head" style="margin-top:4px">' +
          '<h3 class="card-title">客户端下载</h3>' +
          '<span class="muted">直连服务端下载，命令中已包含</span>' +
        '</div>' +
        '<div class="dl-list">' +
          dlKeys.map(function (k) {
            return '<a class="dl-item" href="' + escAttr(dls[k]) + '" target="_blank" rel="noreferrer noopener">' +
              '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><use href="#ic-download"/></svg>' +
              esc(dlLabel(k)) + '</a>';
          }).join('') +
        '</div>';
    }

    var userName = data.user || user || '';
    box.innerHTML =
      '<div class="access-grid">' + grids + '</div>' +
      dlHtml +
      '<div class="access-note">' +
        '<b>给 AI 的说明：</b>把上面任意一条命令直接复制发给对方，在终端或 PowerShell 里一键执行即可完成安装与接入。' +
        '当前用户 <b>' + esc(userName) + '</b>，每条命令里已内嵌该用户专属密钥，请勿外传。' +
        '命令较长时，可在代码块内左右拖动查看完整内容。' +
      '</div>';
  }

  /* ============================== 设置 ============================== */
  function portText(v) {
    var n = Number(v) || 0;
    return n > 0 ? String(n) : '未启用';
  }

  function renderConfig() {
    var grid = $('#cfgGrid');
    if (!grid) return;
    var cfg = (STATE.sys && STATE.sys.config) || {};
    var rows = [
      ['监听端口', portText(cfg.bindPort)],
      ['HTTP 端口', portText(cfg.vhostHTTPPort)],
      ['HTTPS 端口', portText(cfg.vhostHTTPSPort)],
      ['TCPMUX CONNECT 端口', portText(cfg.tcpmuxHTTPConnectPort)],
      ['KCP 端口', portText(cfg.kcpBindPort)],
      ['QUIC 端口', portText(cfg.quicBindPort)],
      ['强制 TLS', cfg.tlsForce ? '已开启' : '未开启'],
      ['心跳超时', cfg.heartbeatTimeout === undefined || cfg.heartbeatTimeout === null ? '--' : (cfg.heartbeatTimeout + ' 秒')],
      ['子域名后缀', cfg.subdomainHost ? String(cfg.subdomainHost) : '未设置'],
      ['端口白名单', cfg.allowPortsStr ? String(cfg.allowPortsStr) : '未限制'],
      ['最大连接池', cfg.maxPoolCount === undefined || cfg.maxPoolCount === null ? '--' : String(cfg.maxPoolCount)],
      ['单客户端最大端口数', Number(cfg.maxPortsPerClient) === 0 ? '不限' : String(cfg.maxPortsPerClient === undefined ? '--' : cfg.maxPortsPerClient)]
    ];
    grid.innerHTML = rows.map(function (r) {
      return '<div class="kv"><span class="kv-k">' + esc(r[0]) + '</span><span class="kv-v">' + esc(r[1]) + '</span></div>';
    }).join('');
    var sv = $('#setVersion');
    if (sv) sv.textContent = STATE.version || '--';
  }

  /* ============================== 数据加载 ============================== */
  function applySystemInfo(info) {
    STATE.sys = info || null;
    STATE.version = (info && info.version) ? String(info.version) : '';
    var verEl = $('#serverVersion');
    if (verEl) verEl.textContent = STATE.version || '--';
    var setVer = $('#setVersion');
    if (setVer) setVer.textContent = STATE.version || '--';

    var st = (info && info.status) || {};
    pushRate(st);

    if (STATE.page === 'overview') {
      renderOverview();
      drawRateChart();
    }
    if (STATE.page === 'settings') renderConfig();
  }

  function loadSystem(silent) {
    return api('/system/info').then(function (info) {
      applySystemInfo(info);
      return info;
    }).catch(function (err) {
      if (!silent) toast(err.message, 'err');
      return null;
    });
  }

  function loadRecent(silent) {
    var r = STATE.recent;
    if (!silent) {
      r.loading = true;
      renderRecent();
    }
    return api('/proxies?page=1&pageSize=5').then(function (data) {
      r.items = Array.isArray(data.items) ? data.items : [];
      r.loading = false;
      renderRecent();
      return data;
    }).catch(function (err) {
      r.loading = false;
      renderRecent();
      if (!silent) toast(err.message, 'err');
      return null;
    });
  }

  /* ============================== 轮询 ============================== */
  function startPoll() {
    stopPoll();
    STATE.timer = setInterval(pollOnce, POLL_INTERVAL);
  }

  function stopPoll() {
    if (STATE.timer) {
      clearInterval(STATE.timer);
      STATE.timer = null;
    }
  }

  function pollOnce() {
    if (!STATE.auth) return;
    if (document.visibilityState === 'hidden') return;
    if (STATE.polling) return;
    STATE.polling = true;
    loadSystem(true).then(function () {
      if (STATE.page === 'proxies') return loadProxies({ silent: true });
      if (STATE.page === 'overview') return loadRecent(true);
      return null;
    }).then(function () {
      STATE.polling = false;
    }, function () {
      STATE.polling = false;
    });
  }

  /* ============================== 页面切换 ============================== */
  function switchPage(name) {
    if (!PAGES[name]) name = 'overview';
    STATE.page = name;

    $$('.nav-item[data-page]').forEach(function (b) {
      b.classList.toggle('is-active', b.getAttribute('data-page') === name);
    });
    $$('.page').forEach(function (sec) {
      sec.classList.toggle('is-active', sec.id === 'page-' + name);
    });
    var t = $('#pageTitle');
    var s = $('#pageSub');
    if (t) t.textContent = PAGES[name].title;
    if (s) s.textContent = PAGES[name].sub;

    if (name === 'overview') {
      renderOverview();
      renderRecent();
      if (!STATE.recent.items.length) loadRecent(true);
      requestAnimationFrame(drawRateChart);
    } else if (name === 'proxies') {
      if (!STATE.proxies.items.length && !STATE.proxies.loading) loadProxies();
      else { renderProxies(); renderProxyPager(); }
    } else if (name === 'users') {
      if (!STATE.users.items.length && !STATE.users.loading) loadUsers();
      else { renderUsers(); renderUserPager(); }
    } else if (name === 'access') {
      ensureAccessUsers(false);
    } else if (name === 'settings') {
      renderConfig();
    }
    window.scrollTo({ top: 0, behavior: 'auto' });
  }

  /* ============================== 登录 / 登出 ============================== */
  function showLogin() {
    var app = $('#appView');
    var login = $('#loginView');
    if (app) app.hidden = true;
    if (login) login.hidden = false;
    var u = $('#loginUser');
    if (u) setTimeout(function () { u.focus(); }, 80);
  }

  function enterApp() {
    var app = $('#appView');
    var login = $('#loginView');
    if (login) login.hidden = true;
    if (app) app.hidden = false;
    var pass = $('#loginPass');
    if (pass) pass.value = '';
    var err = $('#loginError');
    if (err) err.textContent = '';

    STATE.lastSample = null;
    STATE.rate.in.length = 0;
    STATE.rate.out.length = 0;

    switchPage('overview');
    loadRecent();
    startPoll();
  }

  function logout(expired) {
    stopPoll();
    STATE.auth = '';
    STATE.polling = false;
    STATE.lastSample = null;
    STATE.rate.in.length = 0;
    STATE.rate.out.length = 0;
    STATE.proxies.items = [];
    STATE.proxies.selected = new Set();
    STATE.users.items = [];
    STATE.pickerItems = [];
    STATE.sys = null;
    try { sessionStorage.removeItem(AUTH_KEY); } catch (e) {}

    var root = $('#modalRoot');
    if (root) {
      root.hidden = true;
      root.innerHTML = '';
      root.onmousedown = null;
    }
    STATE.modalDone = null;
    showLogin();
    if (expired) toast('登录已失效，请重新登录', 'warn');
  }

  function handleLogin(ev) {
    ev.preventDefault();
    var user = $('#loginUser').value.trim();
    var pass = $('#loginPass').value;
    var errEl = $('#loginError');
    var btn = $('#loginBtn');
    errEl.textContent = '';

    if (!user || !pass) {
      errEl.textContent = '请输入管理账号与密码';
      return;
    }

    STATE.auth = encodeBasic(user + ':' + pass);
    btn.disabled = true;
    btn.textContent = '正在验证…';

    api('/system/info', { noAuthReset: true }).then(function (info) {
      try { sessionStorage.setItem(AUTH_KEY, STATE.auth); } catch (e) {}
      applySystemInfo(info);
      enterApp();
      toast('登录成功，欢迎回来', 'ok');
    }).catch(function (err) {
      STATE.auth = '';
      errEl.textContent = err.message || '登录失败';
    }).then(function () {
      btn.disabled = false;
      btn.textContent = '进入控制台';
    });
  }

  function boot() {
    var errEl = $('#loginError');
    if (errEl) errEl.textContent = '正在恢复登录状态…';
    api('/system/info').then(function (info) {
      if (errEl) errEl.textContent = '';
      applySystemInfo(info);
      enterApp();
    }).catch(function (err) {
      STATE.auth = '';
      try { sessionStorage.removeItem(AUTH_KEY); } catch (e) {}
      if (errEl) errEl.textContent = (err && err.message) || '自动登录失败，请重新输入账号密码';
    });
  }

  /* ============================== 事件 ============================== */
  function hasAttrTarget(e, attr) {
    var el = e.target;
    return el && el.closest ? el.closest('[' + attr + ']') : null;
  }

  function onDocClick(e) {
    var rippleBtn = e.target.closest ? e.target.closest('.btn') : null;
    if (rippleBtn && !rippleBtn.disabled) spawnRipple(rippleBtn, e);

    var copyEl = hasAttrTarget(e, 'data-copy');
    if (copyEl) {
      doCopy(copyEl.getAttribute('data-copy'), '已复制到剪贴板');
      return;
    }

    var delEl = hasAttrTarget(e, 'data-del');
    if (delEl) {
      deleteProxies([delEl.getAttribute('data-del')]);
      return;
    }

    var pgEl = hasAttrTarget(e, 'data-page-go');
    if (pgEl) {
      if (pgEl.disabled) return;
      var n = Number(pgEl.getAttribute('data-page-go'));
      if (!isFinite(n) || n < 1) return;
      if (pgEl.closest('#proxyPager')) {
        STATE.proxies.page = n;
        loadProxies();
      } else if (pgEl.closest('#userPager')) {
        STATE.users.page = n;
        loadUsers();
      }
      return;
    }

    var editEl = hasAttrTarget(e, 'data-u-edit');
    if (editEl) {
      openUserForm('edit', findUser(editEl.getAttribute('data-u-edit')));
      return;
    }

    var tokenEl = hasAttrTarget(e, 'data-u-token');
    if (tokenEl) {
      resetToken(tokenEl.getAttribute('data-u-token'));
      return;
    }

    var accessEl = hasAttrTarget(e, 'data-u-access');
    if (accessEl) {
      var who = accessEl.getAttribute('data-u-access');
      switchPage('access');
      ensureAccessUsers(false).then(function () {
        var sel = $('#accessUser');
        if (sel && who) sel.value = who;
        generateAccess();
      });
      return;
    }

    var userDelEl = hasAttrTarget(e, 'data-u-del');
    if (userDelEl) {
      deleteUser(userDelEl.getAttribute('data-u-del'));
      return;
    }

    var resetEl = hasAttrTarget(e, 'data-u-reset');
    if (resetEl) {
      resetTraffic(resetEl.getAttribute('data-u-reset'));
      return;
    }
  }

  function onDocChange(e) {
    var t = e.target;
    if (!t) return;
    if (t.id === 'proxyAll') {
      STATE.proxies.items.forEach(function (it) {
        if (t.checked) STATE.proxies.selected.add(it.name);
        else STATE.proxies.selected.delete(it.name);
      });
      $$('#proxyTableWrap [data-pick]').forEach(function (cb) { cb.checked = t.checked; });
      syncProxyDeleteBtn();
      return;
    }
    if (t.hasAttribute && t.hasAttribute('data-pick')) {
      var name = t.getAttribute('data-pick');
      if (t.checked) STATE.proxies.selected.add(name);
      else STATE.proxies.selected.delete(name);
      syncProxyDeleteBtn();
    }
  }

  function onKeyDown(e) {
    if (e.key !== 'Escape') return;
    var root = $('#modalRoot');
    if (!root || root.hidden) return;
    if (STATE.modalDone) {
      var fn = STATE.modalDone;
      STATE.modalDone = null;
      fn(false);
      return;
    }
    var closeBtn = $('.modal-close', root);
    if (closeBtn) closeBtn.click();
  }

  function bindEvents() {
    $('#loginForm').addEventListener('submit', handleLogin);

    $('#topLogout').addEventListener('click', function () {
      logout(false);
      toast('已退出登录', 'ok');
    });
    $('#sideLogout').addEventListener('click', function () {
      logout(false);
      toast('已退出登录', 'ok');
    });

    $('#themeToggle').addEventListener('click', toggleTheme);
    $('#themeSwitch').addEventListener('click', toggleTheme);

    $('#nav').addEventListener('click', function (e) {
      var btn = e.target.closest ? e.target.closest('.nav-item[data-page]') : null;
      if (btn) switchPage(btn.getAttribute('data-page'));
    });
    $('#goProxies').addEventListener('click', function () { switchPage('proxies'); });

    $('#proxyRefresh').addEventListener('click', function () {
      STATE.proxies.page = 1;
      loadProxies().then(function () { toast('隧道列表已刷新', 'ok'); });
    });
    $('#proxyType').addEventListener('change', function (e) {
      STATE.proxies.type = e.target.value;
      STATE.proxies.page = 1;
      loadProxies();
    });
    $('#proxyStatus').addEventListener('change', function (e) {
      STATE.proxies.status = e.target.value;
      STATE.proxies.page = 1;
      loadProxies();
    });
    $('#proxyQ').addEventListener('input', debounce(function (e) {
      STATE.proxies.q = e.target.value.trim();
      STATE.proxies.page = 1;
      loadProxies();
    }, 320));

    $('#proxyDelSel').addEventListener('click', function () {
      var names = Array.from(STATE.proxies.selected);
      if (!names.length) { toast('请先勾选需要删除的隧道', 'warn'); return; }
      deleteProxies(names);
    });

    $('#userRefresh').addEventListener('click', function () {
      STATE.users.page = 1;
      loadUsers().then(function () { toast('用户列表已刷新', 'ok'); });
    });
    $('#userNew').addEventListener('click', function () { openUserForm('new', null); });
    $('#userQ').addEventListener('input', debounce(function (e) {
      STATE.users.q = e.target.value.trim();
      STATE.users.page = 1;
      loadUsers();
    }, 320));

    $('#accessGen').addEventListener('click', generateAccess);
    $('#accessReload').addEventListener('click', function () {
      ensureAccessUsers(true).then(function () { toast('用户列表已刷新', 'ok'); });
    });
    $('#accessUser').addEventListener('change', function () {
      var box = $('#accessResult');
      if (box) box.innerHTML = '';
    });

    document.addEventListener('click', onDocClick);
    document.addEventListener('change', onDocChange);
    document.addEventListener('keydown', onKeyDown);

    document.addEventListener('visibilitychange', function () {
      if (document.visibilityState === 'visible' && STATE.auth) pollOnce();
    });

    window.addEventListener('resize', debounce(function () {
      if (STATE.page === 'overview') drawRateChart();
    }, 180));
  }

  /* ============================== 启动 ============================== */
  function init() {
    initTheme();
    buildPetals();
    bindEvents();

    try {
      STATE.auth = sessionStorage.getItem(AUTH_KEY) || '';
    } catch (e) {
      STATE.auth = '';
    }

    if (STATE.auth) boot();
    else showLogin();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

})();
