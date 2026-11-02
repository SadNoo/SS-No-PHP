// 后台节点表单：VLESS REALITY 的 SNI / 公钥 / short ID 输入框与“节点地址”双向同步。
// 输入框只是编辑方式，保存的仍是“节点地址”一个字段；其他选项（target、minver 等）及顺序原样保留，
// short ID 允许为空（保存为 sid=）。外部中转在单独的“中转地址/中转端口”中填写，不经过这里。
(function () {
  'use strict';
  var server = document.getElementById('server');
  var sort = document.getElementById('sort');
  var box = document.getElementById('reality-fields');
  if (!server || !sort || !box) return;
  var keys = ['sni', 'pbk', 'sid'];
  var fields = {};
  keys.forEach(function (k) { fields[k] = document.getElementById('reality_' + k); });

  // host;port;0;tcp;reality;sni=...|pbk=...|sid=...
  function parse(create) {
    var value = server.value;
    if (value.trim() === '' && create) value = ';443;0;tcp;reality;';
    var parts = value.split(';');
    if (parts.length !== 6) return null;
    var opts = parts[5] === '' ? [] : parts[5].split('|').map(function (o) {
      var i = o.indexOf('=');
      return i < 0 ? [o, null] : [o.slice(0, i), o.slice(i + 1)];
    });
    return { parts: parts, opts: opts };
  }

  function fromServer() {
    var p = parse(false);
    keys.forEach(function (k) {
      var v = '';
      if (p) p.opts.forEach(function (o) { if (o[0] === k && o[1] !== null) v = o[1]; });
      fields[k].value = v;
    });
  }

  function toServer() {
    var p = parse(true);
    if (!p) return; // 节点地址不是 6 段格式时不改写，保存时由服务器端给出格式错误
    keys.forEach(function (k) {
      var found = false;
      p.opts.forEach(function (o) { if (o[0] === k) { o[1] = fields[k].value.trim(); found = true; } });
      if (!found) p.opts.push([k, fields[k].value.trim()]);
    });
    p.parts[5] = p.opts.map(function (o) { return o[1] === null ? o[0] : o[0] + '=' + o[1]; }).join('|');
    server.value = p.parts.join(';');
  }

  function toggle() {
    box.hidden = sort.value !== '15';
    if (!box.hidden) fromServer();
  }

  server.addEventListener('input', function () { if (!box.hidden) fromServer(); });
  keys.forEach(function (k) { fields[k].addEventListener('input', toServer); });
  sort.addEventListener('change', toggle);
  toggle();
})();
