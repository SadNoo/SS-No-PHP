/*
 * 节点列表页展开节点时显示的图表（原版 user/nodeajax.tpl 中的内联脚本）。
 * 数据由 /user/node/{id}/ajax 以 <script type="application/json" class="node-chart-data"> 提供。
 */
(function () {
    'use strict';

    function points(list, label) {
        return (list || []).map(function (p) {
            var o = {x: new Date(p.x), y: p.y};
            if (label) {
                o.label = label;
            }
            return o;
        });
    }

    function pct(v) {
        return (Math.round(v * 100) / 100).toFixed(2);
    }

    window.renderNodeCharts = function (container) {
        var el = container.querySelector('script.node-chart-data');
        if (!el || typeof CanvasJS === 'undefined') {
            return;
        }
        var d;
        try {
            d = JSON.parse(el.textContent);
        } catch (e) {
            return;
        }
        var id = d.id, prefix = d.prefix, st = d.speedtest || {};
        var up = d.up_rate * 100, down = (1 - d.up_rate) * 100;

        var charts = [
            new CanvasJS.Chart('load' + id + '_chart', {
                title: {text: '节点负载情况 ' + prefix},
                data: [{type: 'line', dataPoints: points(d.load)}]
            }),
            new CanvasJS.Chart('up' + id + '_chart', {
                title: {text: '最近一天节点在线情况 ' + prefix + ' - 在线 ' + d.uptime},
                data: [{
                    indexLabelFontSize: 20,
                    indexLabelFontFamily: 'Garamond',
                    indexLabelFontColor: 'darkgrey',
                    indexLabelLineColor: 'darkgrey',
                    yValueFormatString: '##0.00"%"',
                    indexLabelPlacement: 'outside',
                    type: 'doughnut',
                    showInLegend: true,
                    dataPoints: [
                        {y: up, label: '在线率', legendText: '在线率 ' + pct(up) + '%', indexLabel: '在线率 ' + pct(up) + '%'},
                        {y: down, label: '离线率', legendText: '离线率 ' + pct(down) + '%', indexLabel: '离线率 ' + pct(down) + '%'}
                    ]
                }]
            }),
            new CanvasJS.Chart('alive' + id + '_chart', {
                title: {text: '最近一天节点在线人数情况 ' + prefix},
                data: [{type: 'line', yValueFormatString: '##0"人"', dataPoints: points(d.alive, '同时在线人数')}]
            }),
            new CanvasJS.Chart('speedtest' + id + '_chart', {
                title: {text: '最近节点测速延时情况报告 ' + prefix},
                axisY: {suffix: ' ms'},
                data: [
                    ['电信延时', 'telecom_ping'], ['联通延时', 'unicom_ping'], ['移动延时', 'cmcc_ping']
                ].map(function (s) {
                    return {type: 'line', showInLegend: true, legendText: s[0], yValueFormatString: '##0"ms"', dataPoints: points(st[s[1]], s[0])};
                })
            }),
            new CanvasJS.Chart('speedtest' + id + '_ping_chart', {
                title: {text: '最近节点测速速度情况报告 ' + prefix},
                axisY: {includeZero: false, suffix: ' Mbps'},
                toolTip: {shared: true},
                data: [
                    ['电信上传', 'telecom_up'], ['电信下载', 'telecom_down'],
                    ['联通上传', 'unicom_up'], ['联通下载', 'unicom_down'],
                    ['移动下载', 'cmcc_down'], ['移动上传', 'cmcc_up']
                ].map(function (s) {
                    return {
                        type: 'line', showInLegend: true, legendText: s[0] + '速度', name: s[0],
                        yValueFormatString: '##0.00"Mb"', dataPoints: points(st[s[1]], s[0])
                    };
                })
            })
        ];
        charts.forEach(function (c) {
            c.render();
        });
    };
})();
