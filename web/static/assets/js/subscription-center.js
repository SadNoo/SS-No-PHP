(function () {
 'use strict';
 const root = document.getElementById('subscription-center');
 if (!root) return;
 // 订阅线路：有备用订阅地址时页面有 #sub-line 下拉框，复制与导入都使用所选线路；记住上次的选择（只存在本机浏览器）。
 const line = document.getElementById('sub-line');
 const lineKey = 'niuniu-sub-line';
 if (line) {
  try {
   const saved = Number(window.localStorage.getItem(lineKey));
   if (Number.isInteger(saved) && saved >= 0 && saved < line.options.length) line.selectedIndex = saved;
  } catch (_) { /* 浏览器禁止本地存储时使用主线路 */ }
 }
 function base() { return line ? line.value : root.dataset.subBase; }
 // vmess 使用 sub=3（VMess 链接列表）；旧正式版的 mu=2 链接仍可使用。
 const queries = {'v2ray':'v2ray=1','clash':'clash=1','surge4':'surge=4','surge5':'surge=5','loon':'loon=1','quantumultx':'quantumultx=1','shadowrocket':'shadowrocket=1','ss2022':'ss2022=1','vmess':'sub=3','vless':'vless=1','anytls':'anytls=1','surfboard':'surfboard=1','quantumult':'quantumult=3','quantumult-source':'quantumult=2','kitsunebi':'kitsunebi=1'};
 function subscription(format) {
  const url = new URL(base(), window.location.href);
  url.search = format === 'sing-box' ? 'sing-box=1' : queries[format];
  if (format === 'sing-box') {
   url.searchParams.set('sb_version', document.getElementById('sb-version').value);
   const mode = document.getElementById('sb-mode').value;
   if (mode.includes('quic')) url.searchParams.set('quic', '1');
   if (mode.includes('tailscale')) url.searchParams.set('tailscale', '1');
  }
  for (const key of ['type','class','noclass','regex','emoji','extend']) {
   const field = document.getElementById('sub-' + key);
   if (field && field.value !== '') url.searchParams.set(key, field.value);
  }
  return url.href;
 }
 function importURL(format, url) {
  const encoded = encodeURIComponent(url);
  switch (format) {
   case 'sing-box': return 'sing-box://import-remote-profile?url=' + encoded + '&name=Subscription';
   case 'surge4': case 'surge5': return 'surge:///install-config?url=' + encoded;
   case 'clash': return 'clash://install-config?url=' + encoded;
   case 'loon': return 'loon://import?sub=' + encoded;
   case 'quantumultx': return 'quantumult-x:///add-resource?remote-resource=' + encodeURIComponent(JSON.stringify({server_remote: [url + ', tag=Subscription']}));
   case 'shadowrocket': return 'sub://' + btoa(url) + '#Subscription';
   case 'v2ray': return 'v2rayng://install-config?url=' + encoded;
   default: return url;
  }
 }
 function update() {
  if (line) {
   try { window.localStorage.setItem(lineKey, String(line.selectedIndex)); } catch (_) { /* 忽略 */ }
  }
  const version = document.getElementById('sb-version');
  document.getElementById('sb-version-caption').textContent = version.options[version.selectedIndex].dataset.caption;
  const mode = document.getElementById('sb-mode').value;
  root.querySelectorAll('[data-sb-mode]').forEach(b => {
   const active = b.dataset.sbMode === mode;
   b.classList.toggle('active', active);
   b.setAttribute('aria-pressed', String(active));
   if (active) document.getElementById('sb-mode-caption').textContent = b.dataset.caption;
  });
  root.querySelectorAll('[data-sub-import]').forEach(a => { a.href = importURL(a.dataset.subImport, subscription(a.dataset.subImport)); });
 }
 async function copy(text) {
  if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text);
  const input = document.createElement('textarea'); input.value = text; root.append(input); input.select();
  const copied = document.execCommand('copy'); input.remove();
  if (!copied) throw new Error('copy failed');
 }
 root.addEventListener('change', update);
 root.addEventListener('input', update);
 root.addEventListener('click', async event => {
  const mode = event.target.closest('[data-sb-mode]');
  if (mode) { document.getElementById('sb-mode').value = mode.dataset.sbMode; update(); return; }
  const button = event.target.closest('[data-sub-copy],[data-copy-text]');
  if (!button) return;
  const all = button.dataset.copyText !== undefined;
  const message = button.closest('.card-inner').querySelector('.sub-message');
  try {
   if (all && button.dataset.copyText === '') { message.textContent = '暂无可用节点。'; return; }
   await copy(all ? button.dataset.copyText : subscription(button.dataset.subCopy));
   message.textContent = all ? '全部节点链接已复制，请勿泄露。' : '订阅链接已复制，请勿泄露。';
  } catch (_) { message.textContent = '复制失败，请检查浏览器的剪贴板权限。'; }
 });
 update();
})();
