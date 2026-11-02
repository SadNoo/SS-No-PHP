/*
 * niuniupanel 公共脚本。
 *
 * 新版启用了严格的内容安全策略（CSP），不允许 onclick="..." 这类内联事件属性。
 * 原版模板中的内联事件统一改写为 data-* 属性，由这里的事件委托实现相同行为：
 *   data-prevent                     点击时阻止默认行为（原 onclick="return false;"）
 *   data-call="函数名" data-args='[...]'  点击时调用全局函数（原 onClick="fn(a, b)"）
 *   data-nospace                     输入框中禁止输入空格（原 onKeypress 拦截空格）
 *
 * 会修改数据的操作（重置订阅链接、重置两步验证密钥等）在原版是普通链接（GET），
 * 可被其他网站诱导触发；新版改为 POST，页面中用 postTo(url) 提交。
 */
(function () {
    'use strict';

    document.addEventListener('click', function (e) {
        // 原版大量使用 href="javascript:void(0);" 作为占位链接，CSP 禁止执行 javascript: 地址，直接阻止跳转。
        var jsLink = e.target.closest('a[href^="javascript:"]');
        if (jsLink) {
            e.preventDefault();
        }
        var el = e.target.closest('[data-prevent], [data-call]');
        if (!el) {
            return;
        }
        if (el.hasAttribute('data-prevent')) {
            e.preventDefault();
        }
        var name = el.getAttribute('data-call');
        if (name && typeof window[name] === 'function') {
            var args = [];
            var raw = el.getAttribute('data-args');
            if (raw) {
                try {
                    args = JSON.parse(raw);
                } catch (err) {
                    console.error('data-args 不是有效的 JSON', raw);
                    return;
                }
            }
            window[name].apply(el, args);
        }
    });

    document.addEventListener('keypress', function (e) {
        if (e.target.hasAttribute && e.target.hasAttribute('data-nospace') && (e.key === ' ' || e.keyCode === 32)) {
            e.preventDefault();
        }
    });

    // 延时跳转（原版使用 setTimeout("location.href='...'", 延时)，字符串形式会被 CSP 拦截）。
    window.jumpTo = function (url, delay) {
        window.setTimeout(function () {
            window.location.href = url;
        }, parseInt(delay, 10) || 0);
    };

    // 以 POST 方式提交到 url（可选延时），服务器处理后按原版重定向。
    window.postTo = function (url, delay) {
        window.setTimeout(function () {
            var form = document.createElement('form');
            form.method = 'post';
            form.action = url;
            form.style.display = 'none';
            document.body.appendChild(form);
            form.submit();
        }, parseInt(delay, 10) || 0);
    };
})();
