-- 演示数据（docker compose 的 seed 服务在面板启动、表结构建好后写入）。可重复执行。
SET NAMES utf8mb4;

-- 帐号：admin@demo.local / DemoAdmin123（管理员），user@demo.local / DemoUser123（普通用户）
INSERT IGNORE INTO user (id,user_name,email,pass,passwd,port,reg_date,im_value,pac,remark,forbidden_ip,forbidden_port,disconnect_ip,
    transfer_enable,u,d,last_day_t,t,money,class,class_expire,expire_in,is_admin,invite_num,method,protocol,obfs,node_connector,last_check_in_time)
VALUES
 (1,'管理员','admin@demo.local','$argon2id$v=19$m=65536,t=3,p=2$ocVFofzPFh+0ZGPtQZoQNw$FgCfxVmNRwOVsS9ZhD6MF+9+sz0i0PczJhkoH6DjnrM','adminpass1',20001,
  UTC_TIMESTAMP(),'','','','','','',1099511627776,0,0,0,0,0.00,10,UTC_TIMESTAMP() + INTERVAL 3650 DAY,'2099-06-04 00:05:00',1,10,'aes-256-cfb','origin','plain',0,0),
 (2,'演示用户','user@demo.local','$argon2id$v=19$m=65536,t=3,p=2$iIK6gFcaaYkDErDqE17ElQ$AdSbxoSXHzSTgw9C3nB7g63I/7yR3o0B4vzTp6L+ugI','userpass12',20002,
  UTC_TIMESTAMP() - INTERVAL 40 DAY,'','','','','','',107374182400,3221225472,12884901888,10737418240,UNIX_TIMESTAMP()-300,25.00,2,
  UTC_TIMESTAMP() + INTERVAL 20 DAY,UTC_TIMESTAMP() + INTERVAL 20 DAY,0,5,'chacha20-ietf','auth_aes128_md5','tls1.2_ticket_auth',3,0);

INSERT IGNORE INTO ss_node (id,name,type,server,method,info,status,sort,traffic_rate,node_class,node_speedlimit,node_heartbeat,node_ip,node_group,custom_rss,mu_only,custom_method,node_bandwidth,node_bandwidth_limit)
VALUES
 (1,'香港 01',1,'hk1.demo.example','aes-256-cfb','香港 BGP 线路','可用',0,1,0,0,UNIX_TIMESTAMP()-30,'198.51.100.1',0,1,0,1,53687091200,1099511627776),
 (2,'日本 中转',1,'jp1.demo.example','aes-256-cfb','经香港中转','可用',10,1.5,1,0,UNIX_TIMESTAMP()-45,'198.51.100.2',0,1,0,1,21474836480,0),
 (3,'美国 V2Ray',1,'us1.demo.example;443;0;ws;tls;path=/v2|host=us1.demo.example','','WebSocket + TLS','可用',11,1,1,0,UNIX_TIMESTAMP()-20,'198.51.100.3',0,1,0,0,0,0),
 (4,'新加坡 01',1,'sg1.demo.example','aes-256-cfb','维护中','维护',0,1,0,0,UNIX_TIMESTAMP()-3600,'198.51.100.4',0,1,0,1,0,0),
 (5,'台湾 高级',1,'tw1.demo.example','aes-256-cfb','等级 5 及以上可用','可用',0,0.5,5,0,UNIX_TIMESTAMP()-10,'198.51.100.5',0,1,0,1,0,0);

INSERT INTO ss_node_info (node_id,uptime,`load`,log_time)
SELECT n.id, 864000, '0.32 0.28 0.25', UNIX_TIMESTAMP() - h.s FROM ss_node n
JOIN (SELECT 60 AS s UNION SELECT 1800 UNION SELECT 3600 UNION SELECT 5400 UNION SELECT 7200) h
WHERE n.id IN (1,2,3,5) AND NOT EXISTS (SELECT 1 FROM ss_node_info LIMIT 1);
INSERT INTO ss_node_online_log (node_id,online_user,log_time)
SELECT n.id, 3 + n.id, UNIX_TIMESTAMP() - h.s FROM ss_node n
JOIN (SELECT 60 AS s UNION SELECT 1800 UNION SELECT 3600 UNION SELECT 5400 UNION SELECT 7200) h
WHERE n.id IN (1,2,3,5) AND NOT EXISTS (SELECT 1 FROM ss_node_online_log LIMIT 1);

INSERT INTO user_traffic_log (user_id,u,d,node_id,rate,traffic,log_time)
SELECT 2, 52428800 * h.k, 209715200 * h.k, 1 + (h.k % 2), 1, CONCAT(250 * h.k, 'MB'), UNIX_TIMESTAMP() - h.k * 3600
FROM (SELECT 1 AS k UNION SELECT 2 UNION SELECT 3 UNION SELECT 4 UNION SELECT 5 UNION SELECT 6 UNION SELECT 8 UNION SELECT 10 UNION SELECT 12 UNION SELECT 20) h
WHERE NOT EXISTS (SELECT 1 FROM user_traffic_log LIMIT 1);

INSERT IGNORE INTO shop (id,name,price,content,auto_renew,auto_reset_bandwidth,status) VALUES
 (1,'月付 · 基础',15.00,'{"bandwidth":"100","expire":"30","class":"2","class_expire":"30","speedlimit":"0","connector":"3","content_extra":"check-全部基础节点;check-3 台设备同时在线;times-高级节点"}',30,1,1),
 (2,'季付 · 基础',40.00,'{"bandwidth":"300","expire":"90","class":"2","class_expire":"90","speedlimit":"0","connector":"3","content_extra":"check-全部基础节点;check-3 台设备同时在线;times-高级节点"}',90,1,1),
 (3,'月付 · 高级',30.00,'{"bandwidth":"300","expire":"30","class":"5","class_expire":"30","speedlimit":"0","connector":"5","content_extra":"check-全部节点（含高级）;check-5 台设备同时在线"}',30,1,1);

INSERT IGNORE INTO coupon (id,code,onetime,expire,shop,credit) VALUES (1,'DEMO20',0,UNIX_TIMESTAMP()+365*86400,'',20);

INSERT INTO announcement (date,content,markdown)
SELECT UTC_TIMESTAMP(), '<p>欢迎试用 <b>niuniupanel</b> 演示站。演示数据为虚构内容，节点不可连接。</p>',
       '欢迎试用 **niuniupanel** 演示站。演示数据为虚构内容，节点不可连接。'
WHERE NOT EXISTS (SELECT 1 FROM announcement LIMIT 1);

INSERT IGNORE INTO code (id,code,type,number,isused,userid,usedatetime) VALUES
 (1,'DEMO-RECHARGE-50',-1,50,0,0,UTC_TIMESTAMP()),
 (2,'后台充值',-1,30,1,2,UTC_TIMESTAMP() - INTERVAL 10 DAY);

INSERT IGNORE INTO ticket (id,title,content,rootid,userid,datetime,status) VALUES
 (1,'日本节点晚高峰速度慢','<p>晚上 9 点后速度明显下降，请帮忙看看。</p>',0,2,UNIX_TIMESTAMP()-86400,1),
 (2,'','<p>已为您调整线路，请再试试。</p>',1,1,UNIX_TIMESTAMP()-80000,1);
