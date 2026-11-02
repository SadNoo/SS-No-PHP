/*
 * 兼容原版 qrcodejs（davidshimjs）的 QRCode 接口，底层使用仍在维护的 qrcode-generator。
 * 支持：new QRCode(元素或元素ID, 文本或选项)、qrcode.makeCode(文本)、qrcode.clear()。
 * 选项：text、width、height（像素），其余选项忽略。
 */
(function (window) {
    'use strict';

    function resolve(el) {
        return typeof el === 'string' ? document.getElementById(el) : el;
    }

    function QRCode(el, opts) {
        this._el = resolve(el);
        if (typeof opts === 'string') {
            opts = {text: opts};
        }
        this._opts = opts || {};
        if (this._opts.text) {
            this.makeCode(this._opts.text);
        }
    }

    QRCode.prototype.makeCode = function (text) {
        if (!this._el) {
            return;
        }
        qrcode.stringToBytes = qrcode.stringToBytesFuncs['UTF-8'];
        var qr = qrcode(0, 'M');
        qr.addData(String(text), 'Byte');
        qr.make();
        var size = this._opts.width || 256;
        var cell = Math.max(1, Math.floor(size / (qr.getModuleCount() + 8)));
        var img = new Image();
        img.src = qr.createDataURL(cell, cell * 4);
        img.alt = '二维码';
        img.style.width = size + 'px';
        img.style.height = (this._opts.height || size) + 'px';
        this.clear();
        this._el.appendChild(img);
    };

    QRCode.prototype.clear = function () {
        if (this._el) {
            while (this._el.firstChild) {
                this._el.removeChild(this._el.firstChild);
            }
        }
    };

    window.QRCode = QRCode;
})(window);
