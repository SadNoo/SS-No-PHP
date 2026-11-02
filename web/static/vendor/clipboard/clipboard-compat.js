// 兼容原版 clipboard.js 1.x 的全局名 Clipboard。
// 现代浏览器自带同名的 Clipboard 接口（不能 new），这里与原版一样直接覆盖。
window.Clipboard = window.ClipboardJS;
