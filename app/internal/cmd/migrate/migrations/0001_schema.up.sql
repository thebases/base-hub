-- +migrate Up

CREATE SEQUENCE IF NOT EXISTS "public"."applications_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."applicationversions_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."configurationapplicationparameters_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."configurationapplications_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."configurationapplicationsettings_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."configurationfiles_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."configurations_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."customers_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."deviceapplicationsettings_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."devicegroups_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."devices_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."groups_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."icons_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."pendingpushes_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."pendingsignup_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."permissions_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."plugin_audit_log_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."plugin_deviceinfo_deviceparams_device_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."plugin_deviceinfo_deviceparams_gps_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."plugin_deviceinfo_deviceparams_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."plugin_deviceinfo_deviceparams_mobile2_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."plugin_deviceinfo_deviceparams_mobile_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."plugin_deviceinfo_deviceparams_wifi_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."plugin_deviceinfo_settings_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."plugin_devicelog_log_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."plugin_devicelog_settings_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."plugin_devicelog_settings_rules_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."plugin_messaging_messages_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."plugin_push_messages_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."plugin_push_schedule_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."plugins_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."pushmessages_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."settings_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."trialkey_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."uploadedfiles_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."usagestats_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."userconfigurationaccess_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."userdevicegroupsaccess_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."userhints_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."userroles_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."userrolesettings_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE SEQUENCE IF NOT EXISTS "public"."users_id_seq" 
INCREMENT 1
MINVALUE  1
MAXVALUE 2147483647
START 1
CACHE 1;

CREATE TABLE IF NOT EXISTS "public"."applicationfilestocopytemp" (
  "url" varchar(500) COLLATE "pg_catalog"."default",
  "?column?" text COLLATE "pg_catalog"."default",
  "newurl" varchar(500) COLLATE "pg_catalog"."default"
)
;

CREATE TABLE IF NOT EXISTS "public"."applications" (
  "id" int4 NOT NULL DEFAULT nextval('applications_id_seq'::regclass),
  "pkg" varchar(100) COLLATE "pg_catalog"."default" NOT NULL,
  "name" varchar(500) COLLATE "pg_catalog"."default" NOT NULL,
  "showicon" bool NOT NULL DEFAULT false,
  "customerid" int8,
  "system" bool NOT NULL DEFAULT false,
  "latestversion" int4,
  "runafterinstall" bool NOT NULL DEFAULT false,
  "type" varchar(10) COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'app'::character varying,
  "icontext" varchar(256) COLLATE "pg_catalog"."default",
  "iconid" int4,
  "runatboot" bool NOT NULL DEFAULT false,
  "usekiosk" bool NOT NULL DEFAULT false,
  "intent" text COLLATE "pg_catalog"."default"
)
;

CREATE TABLE IF NOT EXISTS "public"."applicationversions" (
  "id" int4 NOT NULL DEFAULT nextval('applicationversions_id_seq'::regclass),
  "applicationid" int4 NOT NULL,
  "version" varchar(50) COLLATE "pg_catalog"."default" NOT NULL,
  "url" varchar(500) COLLATE "pg_catalog"."default",
  "apkhash" varchar(100) COLLATE "pg_catalog"."default",
  "split" bool NOT NULL DEFAULT false,
  "urlarmeabi" text COLLATE "pg_catalog"."default",
  "urlarm64" text COLLATE "pg_catalog"."default",
  "versioncode" int4 NOT NULL DEFAULT 0
)
;

CREATE TABLE IF NOT EXISTS "public"."applicationversionstemp" (
  "to_be_deleted" bool,
  "to_be_replaced" bool,
  "id" int4,
  "newapplicationid" int4,
  "newapplicationversionid" int4,
  "newurl" varchar(500) COLLATE "pg_catalog"."default",
  "name" varchar(500) COLLATE "pg_catalog"."default",
  "pkg" varchar(100) COLLATE "pg_catalog"."default",
  "version" varchar(100) COLLATE "pg_catalog"."default",
  "url" varchar(500) COLLATE "pg_catalog"."default",
  "customerid" int8,
  "ismastercustomer" bool,
  "masterappexists" bool,
  "masterversionexists" bool
)
;

CREATE TABLE IF NOT EXISTS "public"."configurationapplicationparameters" (
  "id" int4 NOT NULL DEFAULT nextval('configurationapplicationparameters_id_seq'::regclass),
  "configurationid" int4 NOT NULL,
  "applicationid" int4 NOT NULL,
  "skipversioncheck" bool NOT NULL DEFAULT false
)
;

CREATE TABLE IF NOT EXISTS "public"."configurationapplications" (
  "id" int4 NOT NULL DEFAULT nextval('configurationapplications_id_seq'::regclass),
  "configurationid" int4 NOT NULL,
  "applicationid" int4 NOT NULL,
  "remove" bool NOT NULL DEFAULT false,
  "showicon" bool NOT NULL DEFAULT false,
  "applicationversionid" int4,
  "action" int4 NOT NULL DEFAULT 1,
  "screenorder" int4,
  "keycode" int4,
  "bottom" bool NOT NULL DEFAULT false,
  "longtap" bool NOT NULL DEFAULT false
)
;

CREATE TABLE IF NOT EXISTS "public"."configurationapplicationsettings" (
  "id" int4 NOT NULL DEFAULT nextval('configurationapplicationsettings_id_seq'::regclass),
  "applicationid" int4 NOT NULL,
  "name" varchar(200) COLLATE "pg_catalog"."default" NOT NULL,
  "type" varchar(20) COLLATE "pg_catalog"."default" NOT NULL,
  "value" text COLLATE "pg_catalog"."default",
  "comment" text COLLATE "pg_catalog"."default",
  "readonly" bool NOT NULL DEFAULT false,
  "extrefid" int4 NOT NULL,
  "lastupdate" int8 NOT NULL
)
;

CREATE TABLE IF NOT EXISTS "public"."configurationfiles" (
  "id" int4 NOT NULL DEFAULT nextval('configurationfiles_id_seq'::regclass),
  "configurationid" int4 NOT NULL,
  "description" text COLLATE "pg_catalog"."default",
  "devicepath" text COLLATE "pg_catalog"."default",
  "externalurl" text COLLATE "pg_catalog"."default",
  "filepath" text COLLATE "pg_catalog"."default",
  "checksum" text COLLATE "pg_catalog"."default",
  "remove" bool NOT NULL DEFAULT false,
  "lastupdate" int8 DEFAULT (EXTRACT(epoch FROM now()) * (1000)::numeric),
  "fileid" int4,
  "replacevariables" bool NOT NULL DEFAULT false
)
;

CREATE TABLE IF NOT EXISTS "public"."configurations" (
  "id" int4 NOT NULL DEFAULT nextval('configurations_id_seq'::regclass),
  "name" varchar(100) COLLATE "pg_catalog"."default" NOT NULL,
  "description" text COLLATE "pg_catalog"."default",
  "type" int4 NOT NULL DEFAULT 0,
  "password" varchar(100) COLLATE "pg_catalog"."default",
  "backgroundcolor" varchar(20) COLLATE "pg_catalog"."default",
  "textcolor" varchar(20) COLLATE "pg_catalog"."default",
  "backgroundimageurl" varchar(500) COLLATE "pg_catalog"."default",
  "iconsize" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'SMALL'::text,
  "desktopheader" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'NO_HEADER'::text,
  "usedefaultdesignsettings" bool NOT NULL DEFAULT true,
  "customerid" int8,
  "gps" bool,
  "bluetooth" bool,
  "wifi" bool,
  "mobiledata" bool,
  "mainappid" int4,
  "eventreceivingcomponent" varchar(512) COLLATE "pg_catalog"."default",
  "kioskmode" bool NOT NULL DEFAULT false,
  "qrcodekey" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT md5((random())::text),
  "contentappid" int4,
  "autoupdate" bool NOT NULL DEFAULT false,
  "blockstatusbar" bool NOT NULL DEFAULT false,
  "systemupdatetype" int4 NOT NULL DEFAULT 0,
  "systemupdatefrom" varchar(10) COLLATE "pg_catalog"."default",
  "systemupdateto" varchar(10) COLLATE "pg_catalog"."default",
  "usbstorage" bool,
  "requestupdates" varchar(20) COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'DONOTTRACK'::character varying,
  "pushoptions" varchar(20) COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'mqttWorker'::character varying,
  "autobrightness" bool,
  "brightness" int4 DEFAULT 180,
  "managetimeout" bool DEFAULT false,
  "timeout" int4 DEFAULT 60,
  "lockvolume" bool DEFAULT false,
  "wifissid" varchar(256) COLLATE "pg_catalog"."default",
  "wifipassword" varchar(256) COLLATE "pg_catalog"."default",
  "wifisecuritytype" varchar(16) COLLATE "pg_catalog"."default",
  "passwordmode" varchar(50) COLLATE "pg_catalog"."default",
  "kioskhome" bool,
  "kioskrecents" bool,
  "kiosknotifications" bool,
  "kiosksysteminfo" bool,
  "kioskkeyguard" bool,
  "orientation" int4,
  "rundefaultlauncher" bool,
  "timezone" varchar(200) COLLATE "pg_catalog"."default",
  "allowedclasses" text COLLATE "pg_catalog"."default",
  "newserverurl" text COLLATE "pg_catalog"."default",
  "locksafesettings" bool,
  "disablescreenshots" bool,
  "restrictions" text COLLATE "pg_catalog"."default",
  "defaultfilepath" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT '/'::text,
  "keepalivetime" int4,
  "managevolume" bool,
  "volume" int4,
  "showwifi" bool,
  "mobileenrollment" bool NOT NULL DEFAULT false,
  "desktopheadertemplate" text COLLATE "pg_catalog"."default",
  "kiosklockbuttons" bool,
  "scheduleappupdate" bool,
  "appupdatefrom" varchar(10) COLLATE "pg_catalog"."default",
  "appupdateto" varchar(10) COLLATE "pg_catalog"."default",
  "disablelocation" bool NOT NULL DEFAULT false,
  "apppermissions" varchar(20) COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'GRANTALL'::character varying,
  "permissive" bool,
  "kioskexit" bool DEFAULT true,
  "qrparameters" text COLLATE "pg_catalog"."default",
  "autostartforeground" bool,
  "displaystatus" bool NOT NULL DEFAULT false,
  "encryptdevice" bool NOT NULL DEFAULT false,
  "downloadupdates" varchar(20) COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'UNLIMITED'::character varying,
  "kioskscreenon" bool,
  "launcherurl" text COLLATE "pg_catalog"."default"
)
;

CREATE TABLE IF NOT EXISTS "public"."customers" (
  "id" int4 NOT NULL DEFAULT nextval('customers_id_seq'::regclass),
  "name" varchar(50) COLLATE "pg_catalog"."default" NOT NULL,
  "description" text COLLATE "pg_catalog"."default",
  "filesdir" text COLLATE "pg_catalog"."default" NOT NULL,
  "master" bool NOT NULL DEFAULT false,
  "prefix" varchar(100) COLLATE "pg_catalog"."default" NOT NULL,
  "registrationtime" int8,
  "lastlogintime" int8,
  "accounttype" int4 NOT NULL DEFAULT 0,
  "expirytime" int8,
  "devicelimit" int4 NOT NULL DEFAULT 3,
  "customerstatus" varchar(100) COLLATE "pg_catalog"."default",
  "email" varchar(50) COLLATE "pg_catalog"."default",
  "firstname" varchar(100) COLLATE "pg_catalog"."default",
  "lastname" varchar(100) COLLATE "pg_catalog"."default",
  "language" varchar(100) COLLATE "pg_catalog"."default",
  "inactivestate" int4 NOT NULL DEFAULT 0,
  "pausestate" int4 NOT NULL DEFAULT 0,
  "abandonstate" int4 NOT NULL DEFAULT 0,
  "sizelimit" int4 NOT NULL DEFAULT 100,
  "signupstatus" varchar(100) COLLATE "pg_catalog"."default" DEFAULT 'active'::character varying,
  "signuptoken" varchar(100) COLLATE "pg_catalog"."default"
)
;

CREATE TABLE IF NOT EXISTS "public"."databasechangelog" (
  "id" varchar(255) COLLATE "pg_catalog"."default" NOT NULL,
  "author" varchar(255) COLLATE "pg_catalog"."default" NOT NULL,
  "filename" varchar(255) COLLATE "pg_catalog"."default" NOT NULL,
  "dateexecuted" timestamp(6) NOT NULL,
  "orderexecuted" int4 NOT NULL,
  "exectype" varchar(10) COLLATE "pg_catalog"."default" NOT NULL,
  "md5sum" varchar(35) COLLATE "pg_catalog"."default",
  "description" varchar(255) COLLATE "pg_catalog"."default",
  "comments" varchar(255) COLLATE "pg_catalog"."default",
  "tag" varchar(255) COLLATE "pg_catalog"."default",
  "liquibase" varchar(20) COLLATE "pg_catalog"."default",
  "contexts" varchar(255) COLLATE "pg_catalog"."default",
  "labels" varchar(255) COLLATE "pg_catalog"."default",
  "deployment_id" varchar(10) COLLATE "pg_catalog"."default"
)
;

CREATE TABLE IF NOT EXISTS "public"."databasechangeloglock" (
  "id" int4 NOT NULL,
  "locked" bool NOT NULL,
  "lockgranted" timestamp(6),
  "lockedby" varchar(255) COLLATE "pg_catalog"."default"
)
;

CREATE TABLE IF NOT EXISTS "public"."deviceapplicationsettings" (
  "id" int4 NOT NULL DEFAULT nextval('deviceapplicationsettings_id_seq'::regclass),
  "applicationid" int4 NOT NULL,
  "name" varchar(200) COLLATE "pg_catalog"."default" NOT NULL,
  "type" varchar(20) COLLATE "pg_catalog"."default" NOT NULL,
  "value" text COLLATE "pg_catalog"."default",
  "comment" text COLLATE "pg_catalog"."default",
  "readonly" bool NOT NULL DEFAULT false,
  "extrefid" int4 NOT NULL,
  "lastupdate" int8 NOT NULL
)
;

CREATE TABLE IF NOT EXISTS "public"."devicegroups" (
  "id" int4 NOT NULL DEFAULT nextval('devicegroups_id_seq'::regclass),
  "deviceid" int4 NOT NULL,
  "groupid" int4 NOT NULL
)
;

CREATE TABLE IF NOT EXISTS "public"."devices" (
  "id" int4 NOT NULL DEFAULT nextval('devices_id_seq'::regclass),
  "number" varchar(100) COLLATE "pg_catalog"."default" NOT NULL,
  "description" text COLLATE "pg_catalog"."default",
  "lastupdate" int8 NOT NULL DEFAULT 0,
  "configurationid" int4 NOT NULL,
  "oldconfigurationid" int4,
  "info" text COLLATE "pg_catalog"."default",
  "imei" varchar(50) COLLATE "pg_catalog"."default",
  "phone" varchar(20) COLLATE "pg_catalog"."default",
  "customerid" int8,
  "imeiupdatets" int8,
  "custom1" text COLLATE "pg_catalog"."default",
  "custom2" text COLLATE "pg_catalog"."default",
  "custom3" text COLLATE "pg_catalog"."default",
  "oldnumber" varchar(100) COLLATE "pg_catalog"."default",
  "fastsearch" varchar(100) COLLATE "pg_catalog"."default",
  "enrolltime" int8,
  "infojson" jsonb,
  "publicip" varchar(100) COLLATE "pg_catalog"."default"
)
;

CREATE TABLE IF NOT EXISTS "public"."devicestatuses" (
  "deviceid" int4 NOT NULL,
  "configfilesstatus" varchar(100) COLLATE "pg_catalog"."default",
  "applicationsstatus" varchar(100) COLLATE "pg_catalog"."default"
)
;

CREATE TABLE IF NOT EXISTS "public"."groups" (
  "id" int4 NOT NULL DEFAULT nextval('groups_id_seq'::regclass),
  "name" varchar(100) COLLATE "pg_catalog"."default" NOT NULL,
  "customerid" int8
)
;

CREATE TABLE IF NOT EXISTS "public"."icons" (
  "id" int4 NOT NULL DEFAULT nextval('icons_id_seq'::regclass),
  "customerid" int4 NOT NULL,
  "name" varchar(64) COLLATE "pg_catalog"."default" NOT NULL,
  "fileid" int4 NOT NULL
)
;

CREATE TABLE IF NOT EXISTS "public"."pendingpushes" (
  "id" int4 NOT NULL DEFAULT nextval('pendingpushes_id_seq'::regclass),
  "messageid" int4 NOT NULL,
  "status" int4 NOT NULL DEFAULT 0,
  "createtime" int8 NOT NULL,
  "sendtime" int8
)
;

CREATE TABLE IF NOT EXISTS "public"."pendingsignup" (
  "id" int4 NOT NULL DEFAULT nextval('pendingsignup_id_seq'::regclass),
  "email" varchar(50) COLLATE "pg_catalog"."default" NOT NULL,
  "signuptime" int8,
  "language" varchar(50) COLLATE "pg_catalog"."default",
  "token" varchar(50) COLLATE "pg_catalog"."default"
)
;

CREATE TABLE IF NOT EXISTS "public"."permissions" (
  "id" int4 NOT NULL DEFAULT nextval('permissions_id_seq'::regclass),
  "name" varchar(50) COLLATE "pg_catalog"."default" NOT NULL,
  "description" text COLLATE "pg_catalog"."default",
  "superadmin" bool NOT NULL DEFAULT false
)
;

CREATE TABLE IF NOT EXISTS "public"."plugin_audit_log" (
  "id" int4 NOT NULL DEFAULT nextval('plugin_audit_log_id_seq'::regclass),
  "createtime" int8 NOT NULL DEFAULT (EXTRACT(epoch FROM now()) * (1000)::numeric),
  "customerid" int4,
  "userid" int4,
  "login" varchar(100) COLLATE "pg_catalog"."default",
  "action" varchar(100) COLLATE "pg_catalog"."default",
  "payload" text COLLATE "pg_catalog"."default",
  "ipaddress" varchar(500) COLLATE "pg_catalog"."default",
  "errorcode" int4 DEFAULT 0
)
;

CREATE TABLE IF NOT EXISTS "public"."plugin_deviceinfo_deviceparams" (
  "id" int4 NOT NULL DEFAULT nextval('plugin_deviceinfo_deviceparams_id_seq'::regclass),
  "deviceid" int4 NOT NULL,
  "customerid" int4 NOT NULL,
  "ts" int8 NOT NULL
)
;

CREATE TABLE IF NOT EXISTS "public"."plugin_deviceinfo_deviceparams_device" (
  "id" int4 NOT NULL DEFAULT nextval('plugin_deviceinfo_deviceparams_device_id_seq'::regclass),
  "recordid" int4 NOT NULL,
  "batterylevel" int4,
  "batterycharging" varchar(20) COLLATE "pg_catalog"."default",
  "ip" varchar(50) COLLATE "pg_catalog"."default",
  "keyguard" bool,
  "ringvolume" int4,
  "wifi" bool,
  "mobiledata" bool,
  "gps" bool,
  "bluetooth" bool,
  "usbstorage" bool,
  "memorytotal" int4,
  "memoryavailable" int4
)
;

CREATE TABLE IF NOT EXISTS "public"."plugin_deviceinfo_deviceparams_gps" (
  "id" int4 NOT NULL DEFAULT nextval('plugin_deviceinfo_deviceparams_gps_id_seq'::regclass),
  "recordid" int4 NOT NULL,
  "state" varchar(20) COLLATE "pg_catalog"."default",
  "lat" float8,
  "lon" float8,
  "alt" float8,
  "speed" float8,
  "course" float8
)
;

CREATE TABLE IF NOT EXISTS "public"."plugin_deviceinfo_deviceparams_mobile" (
  "id" int4 NOT NULL DEFAULT nextval('plugin_deviceinfo_deviceparams_mobile_id_seq'::regclass),
  "recordid" int4 NOT NULL,
  "rssi" int4,
  "carrier" varchar(50) COLLATE "pg_catalog"."default",
  "data" bool,
  "ip" varchar(50) COLLATE "pg_catalog"."default",
  "state" varchar(20) COLLATE "pg_catalog"."default",
  "simstate" varchar(20) COLLATE "pg_catalog"."default",
  "tx" int8,
  "rx" int8
)
;

CREATE TABLE IF NOT EXISTS "public"."plugin_deviceinfo_deviceparams_mobile2" (
  "id" int4 NOT NULL DEFAULT nextval('plugin_deviceinfo_deviceparams_mobile2_id_seq'::regclass),
  "recordid" int4 NOT NULL,
  "rssi" int4,
  "carrier" varchar(50) COLLATE "pg_catalog"."default",
  "data" bool,
  "ip" varchar(50) COLLATE "pg_catalog"."default",
  "state" varchar(20) COLLATE "pg_catalog"."default",
  "simstate" varchar(20) COLLATE "pg_catalog"."default",
  "tx" int8,
  "rx" int8
)
;

CREATE TABLE IF NOT EXISTS "public"."plugin_deviceinfo_deviceparams_wifi" (
  "id" int4 NOT NULL DEFAULT nextval('plugin_deviceinfo_deviceparams_wifi_id_seq'::regclass),
  "recordid" int4 NOT NULL,
  "rssi" int4,
  "ssid" varchar(500) COLLATE "pg_catalog"."default",
  "security" varchar(500) COLLATE "pg_catalog"."default",
  "state" varchar(20) COLLATE "pg_catalog"."default",
  "ip" varchar(50) COLLATE "pg_catalog"."default",
  "tx" int8,
  "rx" int8
)
;

CREATE TABLE IF NOT EXISTS "public"."plugin_deviceinfo_settings" (
  "id" int4 NOT NULL DEFAULT nextval('plugin_deviceinfo_settings_id_seq'::regclass),
  "customerid" int4 NOT NULL,
  "datapreserveperiod" int4 NOT NULL DEFAULT 30,
  "senddata" bool NOT NULL DEFAULT false,
  "intervalmins" int4 NOT NULL DEFAULT 15
)
;

CREATE TABLE IF NOT EXISTS "public"."plugin_devicelog_log" (
  "id" int4 NOT NULL DEFAULT nextval('plugin_devicelog_log_id_seq'::regclass),
  "createtime" int8,
  "customerid" int4 NOT NULL,
  "deviceid" int4 NOT NULL,
  "applicationid" int4 NOT NULL,
  "ipaddress" varchar(512) COLLATE "pg_catalog"."default",
  "severity" text COLLATE "pg_catalog"."default",
  "severityorder" int4,
  "message" text COLLATE "pg_catalog"."default"
)
;

CREATE TABLE IF NOT EXISTS "public"."plugin_devicelog_setting_rule_devices" (
  "ruleid" int4 NOT NULL,
  "deviceid" int4 NOT NULL
)
;

CREATE TABLE IF NOT EXISTS "public"."plugin_devicelog_settings" (
  "id" int4 NOT NULL DEFAULT nextval('plugin_devicelog_settings_id_seq'::regclass),
  "customerid" int4 NOT NULL,
  "logspreserveperiod" int4 NOT NULL DEFAULT 30
)
;

CREATE TABLE IF NOT EXISTS "public"."plugin_devicelog_settings_rules" (
  "id" int4 NOT NULL DEFAULT nextval('plugin_devicelog_settings_rules_id_seq'::regclass),
  "settingid" int4 NOT NULL,
  "name" varchar(120) COLLATE "pg_catalog"."default" NOT NULL,
  "active" bool NOT NULL DEFAULT true,
  "applicationid" int4 NOT NULL,
  "severity" text COLLATE "pg_catalog"."default" NOT NULL,
  "filter" text COLLATE "pg_catalog"."default",
  "groupid" int4,
  "configurationid" int4
)
;

CREATE TABLE IF NOT EXISTS "public"."plugin_messaging_messages" (
  "id" int4 NOT NULL DEFAULT nextval('plugin_messaging_messages_id_seq'::regclass),
  "customerid" int4 NOT NULL,
  "deviceid" int4 NOT NULL,
  "ts" int8 NOT NULL,
  "message" varchar(5000) COLLATE "pg_catalog"."default",
  "status" int4 NOT NULL
)
;

CREATE TABLE IF NOT EXISTS "public"."plugin_push_messages" (
  "id" int4 NOT NULL DEFAULT nextval('plugin_push_messages_id_seq'::regclass),
  "customerid" int4 NOT NULL,
  "deviceid" int4 NOT NULL,
  "ts" int8 NOT NULL,
  "messagetype" varchar(255) COLLATE "pg_catalog"."default",
  "payload" text COLLATE "pg_catalog"."default"
)
;

CREATE TABLE IF NOT EXISTS "public"."plugin_push_schedule" (
  "id" int4 NOT NULL DEFAULT nextval('plugin_push_schedule_id_seq'::regclass),
  "customerid" int4 NOT NULL,
  "deviceid" int4 NOT NULL DEFAULT 0,
  "groupid" int4 NOT NULL DEFAULT 0,
  "configurationid" int4 NOT NULL DEFAULT 0,
  "scope" varchar(255) COLLATE "pg_catalog"."default",
  "messagetype" varchar(255) COLLATE "pg_catalog"."default",
  "payload" text COLLATE "pg_catalog"."default",
  "comment" text COLLATE "pg_catalog"."default",
  "min" varchar(1024) COLLATE "pg_catalog"."default",
  "minbit" bit(60),
  "hour" varchar(1024) COLLATE "pg_catalog"."default",
  "hourbit" bit(24),
  "day" varchar(1024) COLLATE "pg_catalog"."default",
  "daybit" bit(31),
  "weekday" varchar(1024) COLLATE "pg_catalog"."default",
  "weekdaybit" bit(7),
  "month" varchar(1024) COLLATE "pg_catalog"."default",
  "monthbit" bit(12)
)
;

CREATE TABLE IF NOT EXISTS "public"."plugins" (
  "id" int4 NOT NULL DEFAULT nextval('plugins_id_seq'::regclass),
  "identifier" varchar(50) COLLATE "pg_catalog"."default" NOT NULL,
  "name" text COLLATE "pg_catalog"."default" NOT NULL,
  "description" text COLLATE "pg_catalog"."default",
  "createtime" timestamp(6) NOT NULL DEFAULT now(),
  "disabled" bool NOT NULL DEFAULT false,
  "javascriptmodulefile" varchar(200) COLLATE "pg_catalog"."default",
  "functionsviewtemplate" varchar(200) COLLATE "pg_catalog"."default",
  "settingsviewtemplate" varchar(200) COLLATE "pg_catalog"."default",
  "namelocalizationkey" varchar(200) COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'plugin.name.not.specified'::character varying,
  "settingspermission" varchar(200) COLLATE "pg_catalog"."default",
  "functionspermission" varchar(200) COLLATE "pg_catalog"."default",
  "devicefunctionspermission" varchar(200) COLLATE "pg_catalog"."default",
  "enabledfordevice" bool NOT NULL DEFAULT false
)
;

CREATE TABLE IF NOT EXISTS "public"."pluginsdisabled" (
  "pluginid" int4 NOT NULL,
  "customerid" int4 NOT NULL
)
;

CREATE TABLE IF NOT EXISTS "public"."pushmessages" (
  "id" int4 NOT NULL DEFAULT nextval('pushmessages_id_seq'::regclass),
  "messagetype" varchar(50) COLLATE "pg_catalog"."default" NOT NULL,
  "deviceid" int4 NOT NULL,
  "payload" text COLLATE "pg_catalog"."default"
)
;

CREATE TABLE IF NOT EXISTS "public"."settings" (
  "id" int4 NOT NULL DEFAULT nextval('settings_id_seq'::regclass),
  "backgroundcolor" varchar(20) COLLATE "pg_catalog"."default",
  "textcolor" varchar(20) COLLATE "pg_catalog"."default",
  "backgroundimageurl" varchar(500) COLLATE "pg_catalog"."default",
  "iconsize" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'SMALL'::text,
  "desktopheader" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'NO_HEADER'::text,
  "customerid" int8,
  "usedefaultlanguage" bool NOT NULL DEFAULT true,
  "language" varchar(20) COLLATE "pg_catalog"."default",
  "createnewdevices" bool NOT NULL DEFAULT false,
  "newdevicegroupid" int4,
  "newdeviceconfigurationid" int4,
  "phonenumberformat" varchar(50) COLLATE "pg_catalog"."default" DEFAULT '+9 (999) 999-99-99'::character varying,
  "custompropertyname1" varchar(200) COLLATE "pg_catalog"."default",
  "custompropertyname2" varchar(200) COLLATE "pg_catalog"."default",
  "custompropertyname3" varchar(200) COLLATE "pg_catalog"."default",
  "custommultiline1" bool NOT NULL DEFAULT false,
  "custommultiline2" bool NOT NULL DEFAULT false,
  "custommultiline3" bool NOT NULL DEFAULT false,
  "customsend1" bool NOT NULL DEFAULT false,
  "customsend2" bool NOT NULL DEFAULT false,
  "customsend3" bool NOT NULL DEFAULT false,
  "desktopheadertemplate" text COLLATE "pg_catalog"."default",
  "senddescription" bool NOT NULL DEFAULT false,
  "passwordreset" bool NOT NULL DEFAULT false,
  "passwordlength" int4 NOT NULL DEFAULT 0,
  "passwordstrength" int4 NOT NULL DEFAULT 0,
  "twofactor" bool NOT NULL DEFAULT false,
  "idlelogout" int4
)
;

CREATE TABLE IF NOT EXISTS "public"."trialkey" (
  "id" int4 NOT NULL DEFAULT nextval('trialkey_id_seq'::regclass),
  "keycode" varchar(50) COLLATE "pg_catalog"."default" NOT NULL,
  "created" timestamp(6) NOT NULL DEFAULT now()
)
;

CREATE TABLE IF NOT EXISTS "public"."uploadedfiles" (
  "id" int4 NOT NULL DEFAULT nextval('uploadedfiles_id_seq'::regclass),
  "customerid" int4 NOT NULL,
  "filepath" text COLLATE "pg_catalog"."default",
  "uploadtime" int8 NOT NULL DEFAULT (EXTRACT(epoch FROM now()) * (1000)::numeric),
  "description" text COLLATE "pg_catalog"."default",
  "devicepath" text COLLATE "pg_catalog"."default",
  "external" bool NOT NULL DEFAULT false,
  "externalurl" text COLLATE "pg_catalog"."default",
  "replacevariables" bool NOT NULL DEFAULT false
)
;

CREATE TABLE IF NOT EXISTS "public"."usagestats" (
  "id" int4 NOT NULL DEFAULT nextval('usagestats_id_seq'::regclass),
  "ts" date NOT NULL DEFAULT CURRENT_DATE,
  "instanceid" varchar(255) COLLATE "pg_catalog"."default",
  "webversion" varchar(255) COLLATE "pg_catalog"."default",
  "community" bool NOT NULL DEFAULT true,
  "devicestotal" int4 NOT NULL DEFAULT 0,
  "devicesonline" int4 NOT NULL DEFAULT 0,
  "cputotal" int4 NOT NULL DEFAULT 0,
  "cpuused" int4 NOT NULL DEFAULT 0,
  "ramtotal" int4 NOT NULL DEFAULT 0,
  "ramused" int4 NOT NULL DEFAULT 0,
  "scheme" varchar(255) COLLATE "pg_catalog"."default",
  "arch" varchar(255) COLLATE "pg_catalog"."default",
  "os" varchar(255) COLLATE "pg_catalog"."default"
)
;

CREATE TABLE IF NOT EXISTS "public"."userconfigurationaccess" (
  "id" int4 NOT NULL DEFAULT nextval('userconfigurationaccess_id_seq'::regclass),
  "userid" int4 NOT NULL,
  "configurationid" int4 NOT NULL
)
;

CREATE TABLE IF NOT EXISTS "public"."userdevicegroupsaccess" (
  "id" int4 NOT NULL DEFAULT nextval('userdevicegroupsaccess_id_seq'::regclass),
  "userid" int4 NOT NULL,
  "groupid" int4 NOT NULL
)
;

CREATE TABLE IF NOT EXISTS "public"."userhints" (
  "id" int4 NOT NULL DEFAULT nextval('userhints_id_seq'::regclass),
  "userid" int4 NOT NULL,
  "hintkey" varchar(100) COLLATE "pg_catalog"."default" NOT NULL,
  "created" timestamp(6) NOT NULL DEFAULT now()
)
;

CREATE TABLE IF NOT EXISTS "public"."userhinttypes" (
  "hintkey" varchar(100) COLLATE "pg_catalog"."default" NOT NULL
)
;

CREATE TABLE IF NOT EXISTS "public"."userrolepermissions" (
  "roleid" int4 NOT NULL,
  "permissionid" int4 NOT NULL
)
;

CREATE TABLE IF NOT EXISTS "public"."userroles" (
  "id" int4 NOT NULL DEFAULT nextval('userroles_id_seq'::regclass),
  "name" varchar(50) COLLATE "pg_catalog"."default" NOT NULL,
  "description" text COLLATE "pg_catalog"."default",
  "superadmin" bool NOT NULL DEFAULT false
)
;

CREATE TABLE IF NOT EXISTS "public"."userrolesettings" (
  "id" int4 NOT NULL DEFAULT nextval('userrolesettings_id_seq'::regclass),
  "roleid" int4 NOT NULL,
  "customerid" int4 NOT NULL,
  "columndisplayeddevicestatus" bool,
  "columndisplayeddevicedate" bool,
  "columndisplayeddevicenumber" bool,
  "columndisplayeddevicemodel" bool,
  "columndisplayeddevicepermissionsstatus" bool,
  "columndisplayeddeviceappinstallstatus" bool,
  "columndisplayeddeviceconfiguration" bool,
  "columndisplayeddeviceimei" bool,
  "columndisplayeddevicephone" bool,
  "columndisplayeddevicedesc" bool,
  "columndisplayeddevicegroup" bool,
  "columndisplayedlauncherversion" bool,
  "columndisplayedbatterylevel" bool,
  "columndisplayeddevicefilesstatus" bool,
  "columndisplayeddefaultlauncher" bool,
  "columndisplayedcustom1" bool,
  "columndisplayedcustom2" bool,
  "columndisplayedcustom3" bool,
  "columndisplayedmdmmode" bool,
  "columndisplayedkioskmode" bool,
  "columndisplayedandroidversion" bool,
  "columndisplayedenrollmentdate" bool,
  "columndisplayedserial" bool,
  "columndisplayedpublicip" bool
)
;

CREATE TABLE IF NOT EXISTS "public"."users" (
  "id" int4 NOT NULL DEFAULT nextval('users_id_seq'::regclass),
  "login" varchar(30) COLLATE "pg_catalog"."default" NOT NULL,
  "email" varchar(50) COLLATE "pg_catalog"."default",
  "name" varchar(50) COLLATE "pg_catalog"."default",
  "password" varchar(40) COLLATE "pg_catalog"."default" NOT NULL,
  "customerid" int8,
  "userroleid" int4,
  "alldevicesavailable" bool NOT NULL DEFAULT true,
  "allconfigavailable" bool NOT NULL DEFAULT true,
  "passwordreset" bool NOT NULL DEFAULT false,
  "authtoken" varchar(40) COLLATE "pg_catalog"."default",
  "passwordresettoken" varchar(40) COLLATE "pg_catalog"."default",
  "authdata" text COLLATE "pg_catalog"."default",
  "twofactorsecret" text COLLATE "pg_catalog"."default",
  "twofactoraccepted" bool NOT NULL DEFAULT false,
  "lastloginfail" int8 NOT NULL DEFAULT 0
)
;

CREATE INDEX IF NOT EXISTS "applications_pkg_idx" ON "public"."applications" USING btree (
  "pkg" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);

CREATE INDEX IF NOT EXISTS "configurations_customerid_idx" ON "public"."configurations" USING btree (
  "customerid" "pg_catalog"."int8_ops" ASC NULLS LAST
);

CREATE INDEX IF NOT EXISTS "configurations_mainappid_idx" ON "public"."configurations" USING btree (
  "mainappid" "pg_catalog"."int4_ops" ASC NULLS LAST
);

CREATE INDEX IF NOT EXISTS "devices_groupid_idx" ON "public"."devicegroups" USING btree (
  "groupid" "pg_catalog"."int4_ops" ASC NULLS LAST
);

CREATE INDEX IF NOT EXISTS "devices_customerid_idx" ON "public"."devices" USING btree (
  "customerid" "pg_catalog"."int8_ops" ASC NULLS LAST
);

CREATE INDEX IF NOT EXISTS "devices_fastsearch_idx" ON "public"."devices" USING btree (
  "fastsearch" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);

CREATE INDEX IF NOT EXISTS "devices_number_idx" ON "public"."devices" USING btree (
  "number" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);

DO $$
DECLARE
    rec record;
BEGIN
    FOR rec IN
        SELECT *
        FROM (VALUES
            ('applications', 'applications_pr_key', '("id")'),
            ('applicationversions', 'applicationversions_pr_key', '("id")'),
            ('configurationapplicationparameters', 'configuration_application_parameters_pr_key', '("id")'),
            ('configurationapplications', 'configuration_applications_pr_key', '("id")'),
            ('configurationapplicationsettings', 'configurationapplicationsettings_pr_key', '("id")'),
            ('configurationfiles', 'configurationfiles_pr_key', '("id")'),
            ('configurations', 'configurations_pr_key', '("id")'),
            ('customers', 'customers_pr_key', '("id")'),
            ('databasechangeloglock', 'databasechangeloglock_pkey', '("id")'),
            ('deviceapplicationsettings', 'deviceapplicationsettings_pr_key', '("id")'),
            ('devicegroups', 'devicegroups_pr_key', '("id")'),
            ('devices', 'devices_pr_key', '("id")'),
            ('devicestatuses', 'devicestatuses_pr_key', '("deviceid")'),
            ('groups', 'groups_pr_key', '("id")'),
            ('icons', 'icons_pr_key', '("id")'),
            ('pendingpushes', 'pending_push_pr_key', '("id")'),
            ('pendingsignup', 'pendingsignup_pr_key', '("id")'),
            ('permissions', 'permissions_pr_key', '("id")'),
            ('plugin_audit_log', 'plugin_audit_log_pr_key', '("id")'),
            ('plugin_deviceinfo_deviceparams', 'plugin_deviceinfo_deviceparams_pr_key', '("id")'),
            ('plugin_deviceinfo_deviceparams_device', 'plugin_deviceinfo_deviceparams_device_pr_key', '("id")'),
            ('plugin_deviceinfo_deviceparams_gps', 'plugin_deviceinfo_deviceparams_gps_pr_key', '("id")'),
            ('plugin_deviceinfo_deviceparams_mobile', 'plugin_deviceinfo_deviceparams_mobile_pr_key', '("id")'),
            ('plugin_deviceinfo_deviceparams_mobile2', 'plugin_deviceinfo_deviceparams_mobile2_pr_key', '("id")'),
            ('plugin_deviceinfo_deviceparams_wifi', 'plugin_deviceinfo_deviceparams_wifi_pr_key', '("id")'),
            ('plugin_deviceinfo_settings', 'plugin_deviceinfo_settings_pr_key', '("id")'),
            ('plugin_devicelog_log', 'plugin_devicelog_log_pr_key', '("id")'),
            ('plugin_devicelog_settings', 'plugin_devicelog_settings_pr_key', '("id")'),
            ('plugin_devicelog_settings_rules', 'plugin_devicelog_settings_rules_pr_key', '("id")'),
            ('plugin_messaging_messages', 'plugin_messaging_messages_pr_key', '("id")'),
            ('plugin_push_messages', 'plugin_push_messages_pr_key', '("id")'),
            ('plugin_push_schedule', 'plugin_push_schedule_pr_key', '("id")'),
            ('plugins', 'plugins_pr_key', '("id")'),
            ('pushmessages', 'push_message_pr_key', '("id")'),
            ('settings', 'settings_pr_key', '("id")'),
            ('trialkey', 'trialkey_pr_key', '("id")'),
            ('uploadedfiles', 'uploadedfiles_pr_key', '("id")'),
            ('usagestats', 'usagestats_pr_key', '("id")'),
            ('userconfigurationaccess', 'userconfigurationaccess_pr_key', '("id")'),
            ('userdevicegroupsaccess', 'userdevicegroupsaccess_pr_key', '("id")'),
            ('userhints', 'userhints_pr_key', '("id")'),
            ('userroles', 'roles_pr_key', '("id")'),
            ('userrolesettings', 'userrolesettings_pr_key', '("id")'),
            ('users', 'users_pr_key', '("id")')
        ) AS t(table_name, constraint_name, columns_sql)
    LOOP
        IF NOT EXISTS (
            SELECT 1
            FROM pg_constraint
            WHERE conrelid = format('public.%I', rec.table_name)::regclass
              AND contype = 'p'
        ) THEN
            EXECUTE format(
                'ALTER TABLE public.%I ADD CONSTRAINT %I PRIMARY KEY %s',
                rec.table_name, rec.constraint_name, rec.columns_sql
            );
        END IF;
    END LOOP;
EXCEPTION
    WHEN undefined_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."customers" ADD CONSTRAINT "customer_filesdir_key" UNIQUE ("filesdir");
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."customers" ADD CONSTRAINT "customers_prefix_key" UNIQUE ("prefix");
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."users" ADD CONSTRAINT "users_login_unique" UNIQUE ("login");
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."applications" ADD CONSTRAINT "applications_latestversion_fkey" FOREIGN KEY ("latestversion") REFERENCES "public"."applicationversions" ("id") ON DELETE SET NULL ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."applications" ADD CONSTRAINT "fk_customer_1" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."configurationapplicationparameters" ADD CONSTRAINT "configurationapplicationparameters_configurationid_fkey" FOREIGN KEY ("configurationid") REFERENCES "public"."configurations" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."configurationapplications" ADD CONSTRAINT "configurationapplications_applicationversionid_fkey" FOREIGN KEY ("applicationversionid") REFERENCES "public"."applicationversions" ("id") ON DELETE RESTRICT ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."configurationapplications" ADD CONSTRAINT "configurationapplications_configurationid_fkey" FOREIGN KEY ("configurationid") REFERENCES "public"."configurations" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."configurationapplicationsettings" ADD CONSTRAINT "configurationapplicationsettings_extrefid_fkey" FOREIGN KEY ("extrefid") REFERENCES "public"."configurations" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."configurationfiles" ADD CONSTRAINT "configurationfiles_fileid_fkey" FOREIGN KEY ("fileid") REFERENCES "public"."uploadedfiles" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."configurations" ADD CONSTRAINT "configurations_mainappid_fkey" FOREIGN KEY ("mainappid") REFERENCES "public"."applicationversions" ("id") ON DELETE RESTRICT ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."configurations" ADD CONSTRAINT "fk_customer_2" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."deviceapplicationsettings" ADD CONSTRAINT "deviceapplicationsettings_extrefid_fkey" FOREIGN KEY ("extrefid") REFERENCES "public"."devices" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."devicegroups" ADD CONSTRAINT "devicegroups_groupid_fkey" FOREIGN KEY ("groupid") REFERENCES "public"."groups" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."icons" ADD CONSTRAINT "icons_fileid_fkey" FOREIGN KEY ("fileid") REFERENCES "public"."uploadedfiles" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."plugin_deviceinfo_deviceparams" ADD CONSTRAINT "plugin_deviceinfo_deviceparams_deviceid_fkey" FOREIGN KEY ("deviceid") REFERENCES "public"."devices" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."plugin_devicelog_log" ADD CONSTRAINT "plugin_devicelog_log_customerid_fkey" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."plugin_devicelog_log" ADD CONSTRAINT "plugin_devicelog_log_deviceid_fkey" FOREIGN KEY ("deviceid") REFERENCES "public"."devices" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."plugin_devicelog_setting_rule_devices" ADD CONSTRAINT "plugin_devicelog_setting_rule_devices_ruleid_fkey" FOREIGN KEY ("ruleid") REFERENCES "public"."plugin_devicelog_settings_rules" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."plugin_devicelog_settings_rules" ADD CONSTRAINT "plugin_devicelog_settings_rules_configurationid_fkey" FOREIGN KEY ("configurationid") REFERENCES "public"."configurations" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."plugin_devicelog_settings_rules" ADD CONSTRAINT "plugin_devicelog_settings_rules_groupid_fkey" FOREIGN KEY ("groupid") REFERENCES "public"."groups" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."plugin_devicelog_settings_rules" ADD CONSTRAINT "plugin_devicelog_settings_rules_settingid_fkey" FOREIGN KEY ("settingid") REFERENCES "public"."plugin_devicelog_settings" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."plugin_messaging_messages" ADD CONSTRAINT "plugin_messaging_messages_deviceid_fkey" FOREIGN KEY ("deviceid") REFERENCES "public"."devices" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."plugin_push_messages" ADD CONSTRAINT "plugin_push_messages_deviceid_fkey" FOREIGN KEY ("deviceid") REFERENCES "public"."devices" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."pluginsdisabled" ADD CONSTRAINT "pluginsdisabled_pluginid_fkey" FOREIGN KEY ("pluginid") REFERENCES "public"."plugins" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."userconfigurationaccess" ADD CONSTRAINT "userconfigurationaccess_userid_fkey" FOREIGN KEY ("userid") REFERENCES "public"."users" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."userdevicegroupsaccess" ADD CONSTRAINT "userdevicegroupsaccess_userid_fkey" FOREIGN KEY ("userid") REFERENCES "public"."users" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."userrolepermissions" ADD CONSTRAINT "userrolepermissions_roleid_fkey" FOREIGN KEY ("roleid") REFERENCES "public"."userroles" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."userrolesettings" ADD CONSTRAINT "userrolesettings_roleid_fkey" FOREIGN KEY ("roleid") REFERENCES "public"."userroles" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE "public"."users" ADD CONSTRAINT "users_userroleid_fkey" FOREIGN KEY ("userroleid") REFERENCES "public"."userroles" ("id") ON DELETE RESTRICT ON UPDATE NO ACTION;
EXCEPTION
    WHEN duplicate_object OR duplicate_table THEN
        NULL;
END $$;

DO $$
DECLARE
    rec record;
BEGIN
    FOR rec IN
        SELECT *
        FROM (VALUES
            ('applicationversions', 'applicationversions_app_version_key', 'u', 'ALTER TABLE "public"."applicationversions" ADD CONSTRAINT "applicationversions_app_version_key" UNIQUE ("applicationid", "version");'),
            ('configurationapplicationparameters', 'cap_config_application_unique', 'u', 'ALTER TABLE "public"."configurationapplicationparameters" ADD CONSTRAINT "cap_config_application_unique" UNIQUE ("configurationid", "applicationid");'),
            ('configurations', 'qrcodekey_uniq', 'u', 'ALTER TABLE "public"."configurations" ADD CONSTRAINT "qrcodekey_uniq" UNIQUE ("qrcodekey");'),
            ('customers', 'customer_name_key', 'u', 'ALTER TABLE "public"."customers" ADD CONSTRAINT "customer_name_key" UNIQUE ("name");'),
            ('customers', 'customer_filesdir_key', 'u', 'ALTER TABLE "public"."customers" ADD CONSTRAINT "customer_filesdir_key" UNIQUE ("filesdir");'),
            ('customers', 'customers_prefix_key', 'u', 'ALTER TABLE "public"."customers" ADD CONSTRAINT "customers_prefix_key" UNIQUE ("prefix");'),
            ('devices', 'devices_number_unique', 'u', 'ALTER TABLE "public"."devices" ADD CONSTRAINT "devices_number_unique" UNIQUE ("number");'),
            ('pendingpushes', 'pendingpushes_messageid_key', 'u', 'ALTER TABLE "public"."pendingpushes" ADD CONSTRAINT "pendingpushes_messageid_key" UNIQUE ("messageid");'),
            ('pendingsignup', 'pendingsignup_email_key', 'u', 'ALTER TABLE "public"."pendingsignup" ADD CONSTRAINT "pendingsignup_email_key" UNIQUE ("email");'),
            ('plugin_deviceinfo_deviceparams_device', 'plugin_deviceinfo_deviceparams_device_recordid_key', 'u', 'ALTER TABLE "public"."plugin_deviceinfo_deviceparams_device" ADD CONSTRAINT "plugin_deviceinfo_deviceparams_device_recordid_key" UNIQUE ("recordid");'),
            ('plugin_deviceinfo_deviceparams_gps', 'plugin_deviceinfo_deviceparams_gps_recordid_key', 'u', 'ALTER TABLE "public"."plugin_deviceinfo_deviceparams_gps" ADD CONSTRAINT "plugin_deviceinfo_deviceparams_gps_recordid_key" UNIQUE ("recordid");'),
            ('plugin_deviceinfo_deviceparams_mobile', 'plugin_deviceinfo_deviceparams_mobile_recordid_key', 'u', 'ALTER TABLE "public"."plugin_deviceinfo_deviceparams_mobile" ADD CONSTRAINT "plugin_deviceinfo_deviceparams_mobile_recordid_key" UNIQUE ("recordid");'),
            ('plugin_deviceinfo_deviceparams_mobile2', 'plugin_deviceinfo_deviceparams_mobile2_recordid_key', 'u', 'ALTER TABLE "public"."plugin_deviceinfo_deviceparams_mobile2" ADD CONSTRAINT "plugin_deviceinfo_deviceparams_mobile2_recordid_key" UNIQUE ("recordid");'),
            ('plugin_deviceinfo_deviceparams_wifi', 'plugin_deviceinfo_deviceparams_wifi_recordid_key', 'u', 'ALTER TABLE "public"."plugin_deviceinfo_deviceparams_wifi" ADD CONSTRAINT "plugin_deviceinfo_deviceparams_wifi_recordid_key" UNIQUE ("recordid");'),
            ('plugin_deviceinfo_settings', 'plugin_deviceinfo_settings_customer_unique', 'u', 'ALTER TABLE "public"."plugin_deviceinfo_settings" ADD CONSTRAINT "plugin_deviceinfo_settings_customer_unique" UNIQUE ("customerid");'),
            ('plugins', 'plugin_identifier_unq', 'u', 'ALTER TABLE "public"."plugins" ADD CONSTRAINT "plugin_identifier_unq" UNIQUE ("identifier");'),
            ('settings', 'settings_customer_unique', 'u', 'ALTER TABLE "public"."settings" ADD CONSTRAINT "settings_customer_unique" UNIQUE ("customerid");'),
            ('usagestats', 'usagestats_ts_instanceid_key', 'u', 'ALTER TABLE "public"."usagestats" ADD CONSTRAINT "usagestats_ts_instanceid_key" UNIQUE ("ts", "instanceid");'),
            ('userhints', 'userhints_userid_hintkey_unique', 'u', 'ALTER TABLE "public"."userhints" ADD CONSTRAINT "userhints_userid_hintkey_unique" UNIQUE ("userid", "hintkey");'),
            ('userhinttypes', 'userhinttypes_hintkey_key', 'u', 'ALTER TABLE "public"."userhinttypes" ADD CONSTRAINT "userhinttypes_hintkey_key" UNIQUE ("hintkey");'),
            ('userrolesettings', 'userrolesettings_role_customer_uniq', 'u', 'ALTER TABLE "public"."userrolesettings" ADD CONSTRAINT "userrolesettings_role_customer_uniq" UNIQUE ("roleid", "customerid");'),
            ('users', 'login_key', 'u', 'ALTER TABLE "public"."users" ADD CONSTRAINT "login_key" UNIQUE ("login");'),
            ('users', 'users_login_unique', 'u', 'ALTER TABLE "public"."users" ADD CONSTRAINT "users_login_unique" UNIQUE ("login");'),
            ('applications', 'applications_iconid_fkey', 'f', 'ALTER TABLE "public"."applications" ADD CONSTRAINT "applications_iconid_fkey" FOREIGN KEY ("iconid") REFERENCES "public"."icons" ("id") ON DELETE SET NULL ON UPDATE NO ACTION;'),
            ('applications', 'applications_latestversion_fkey', 'f', 'ALTER TABLE "public"."applications" ADD CONSTRAINT "applications_latestversion_fkey" FOREIGN KEY ("latestversion") REFERENCES "public"."applicationversions" ("id") ON DELETE SET NULL ON UPDATE NO ACTION;'),
            ('applications', 'fk_customer_1', 'f', 'ALTER TABLE "public"."applications" ADD CONSTRAINT "fk_customer_1" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('applicationversions', 'applicationversions_applicationid_fkey', 'f', 'ALTER TABLE "public"."applicationversions" ADD CONSTRAINT "applicationversions_applicationid_fkey" FOREIGN KEY ("applicationid") REFERENCES "public"."applications" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('configurationapplicationparameters', 'configurationapplicationparameters_applicationid_fkey', 'f', 'ALTER TABLE "public"."configurationapplicationparameters" ADD CONSTRAINT "configurationapplicationparameters_applicationid_fkey" FOREIGN KEY ("applicationid") REFERENCES "public"."applications" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('configurationapplicationparameters', 'configurationapplicationparameters_configurationid_fkey', 'f', 'ALTER TABLE "public"."configurationapplicationparameters" ADD CONSTRAINT "configurationapplicationparameters_configurationid_fkey" FOREIGN KEY ("configurationid") REFERENCES "public"."configurations" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('configurationapplications', 'configurationapplications_applicationid_fkey', 'f', 'ALTER TABLE "public"."configurationapplications" ADD CONSTRAINT "configurationapplications_applicationid_fkey" FOREIGN KEY ("applicationid") REFERENCES "public"."applications" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('configurationapplications', 'configurationapplications_applicationversionid_fkey', 'f', 'ALTER TABLE "public"."configurationapplications" ADD CONSTRAINT "configurationapplications_applicationversionid_fkey" FOREIGN KEY ("applicationversionid") REFERENCES "public"."applicationversions" ("id") ON DELETE RESTRICT ON UPDATE NO ACTION;'),
            ('configurationapplications', 'configurationapplications_configurationid_fkey', 'f', 'ALTER TABLE "public"."configurationapplications" ADD CONSTRAINT "configurationapplications_configurationid_fkey" FOREIGN KEY ("configurationid") REFERENCES "public"."configurations" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('configurationapplicationsettings', 'configurationapplicationsettings_applicationid_fkey', 'f', 'ALTER TABLE "public"."configurationapplicationsettings" ADD CONSTRAINT "configurationapplicationsettings_applicationid_fkey" FOREIGN KEY ("applicationid") REFERENCES "public"."applications" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('configurationapplicationsettings', 'configurationapplicationsettings_extrefid_fkey', 'f', 'ALTER TABLE "public"."configurationapplicationsettings" ADD CONSTRAINT "configurationapplicationsettings_extrefid_fkey" FOREIGN KEY ("extrefid") REFERENCES "public"."configurations" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('configurationfiles', 'configurationfiles_configurationid_fkey', 'f', 'ALTER TABLE "public"."configurationfiles" ADD CONSTRAINT "configurationfiles_configurationid_fkey" FOREIGN KEY ("configurationid") REFERENCES "public"."configurations" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('configurationfiles', 'configurationfiles_fileid_fkey', 'f', 'ALTER TABLE "public"."configurationfiles" ADD CONSTRAINT "configurationfiles_fileid_fkey" FOREIGN KEY ("fileid") REFERENCES "public"."uploadedfiles" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('configurations', 'configurations_contentappid_fkey', 'f', 'ALTER TABLE "public"."configurations" ADD CONSTRAINT "configurations_contentappid_fkey" FOREIGN KEY ("contentappid") REFERENCES "public"."applicationversions" ("id") ON DELETE RESTRICT ON UPDATE NO ACTION;'),
            ('configurations', 'configurations_mainappid_fkey', 'f', 'ALTER TABLE "public"."configurations" ADD CONSTRAINT "configurations_mainappid_fkey" FOREIGN KEY ("mainappid") REFERENCES "public"."applicationversions" ("id") ON DELETE RESTRICT ON UPDATE NO ACTION;'),
            ('configurations', 'fk_customer_2', 'f', 'ALTER TABLE "public"."configurations" ADD CONSTRAINT "fk_customer_2" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('deviceapplicationsettings', 'deviceapplicationsettings_applicationid_fkey', 'f', 'ALTER TABLE "public"."deviceapplicationsettings" ADD CONSTRAINT "deviceapplicationsettings_applicationid_fkey" FOREIGN KEY ("applicationid") REFERENCES "public"."applications" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('deviceapplicationsettings', 'deviceapplicationsettings_extrefid_fkey', 'f', 'ALTER TABLE "public"."deviceapplicationsettings" ADD CONSTRAINT "deviceapplicationsettings_extrefid_fkey" FOREIGN KEY ("extrefid") REFERENCES "public"."devices" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('devicegroups', 'devicegroups_deviceid_fkey', 'f', 'ALTER TABLE "public"."devicegroups" ADD CONSTRAINT "devicegroups_deviceid_fkey" FOREIGN KEY ("deviceid") REFERENCES "public"."devices" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('devicegroups', 'devicegroups_groupid_fkey', 'f', 'ALTER TABLE "public"."devicegroups" ADD CONSTRAINT "devicegroups_groupid_fkey" FOREIGN KEY ("groupid") REFERENCES "public"."groups" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('devices', 'fk_customer_3', 'f', 'ALTER TABLE "public"."devices" ADD CONSTRAINT "fk_customer_3" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('devicestatuses', 'devicestatuses_deviceid_fkey', 'f', 'ALTER TABLE "public"."devicestatuses" ADD CONSTRAINT "devicestatuses_deviceid_fkey" FOREIGN KEY ("deviceid") REFERENCES "public"."devices" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('groups', 'fk_customer_4', 'f', 'ALTER TABLE "public"."groups" ADD CONSTRAINT "fk_customer_4" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('icons', 'icons_customerid_fkey', 'f', 'ALTER TABLE "public"."icons" ADD CONSTRAINT "icons_customerid_fkey" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('icons', 'icons_fileid_fkey', 'f', 'ALTER TABLE "public"."icons" ADD CONSTRAINT "icons_fileid_fkey" FOREIGN KEY ("fileid") REFERENCES "public"."uploadedfiles" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('pendingpushes', 'pendingpushes_messageid_fkey', 'f', 'ALTER TABLE "public"."pendingpushes" ADD CONSTRAINT "pendingpushes_messageid_fkey" FOREIGN KEY ("messageid") REFERENCES "public"."pushmessages" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_deviceinfo_deviceparams', 'plugin_deviceinfo_deviceparams_customerid_fkey', 'f', 'ALTER TABLE "public"."plugin_deviceinfo_deviceparams" ADD CONSTRAINT "plugin_deviceinfo_deviceparams_customerid_fkey" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_deviceinfo_deviceparams', 'plugin_deviceinfo_deviceparams_deviceid_fkey', 'f', 'ALTER TABLE "public"."plugin_deviceinfo_deviceparams" ADD CONSTRAINT "plugin_deviceinfo_deviceparams_deviceid_fkey" FOREIGN KEY ("deviceid") REFERENCES "public"."devices" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_deviceinfo_deviceparams_device', 'plugin_deviceinfo_deviceparams_device_recordid_fkey', 'f', 'ALTER TABLE "public"."plugin_deviceinfo_deviceparams_device" ADD CONSTRAINT "plugin_deviceinfo_deviceparams_device_recordid_fkey" FOREIGN KEY ("recordid") REFERENCES "public"."plugin_deviceinfo_deviceparams" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_deviceinfo_deviceparams_gps', 'plugin_deviceinfo_deviceparams_gps_recordid_fkey', 'f', 'ALTER TABLE "public"."plugin_deviceinfo_deviceparams_gps" ADD CONSTRAINT "plugin_deviceinfo_deviceparams_gps_recordid_fkey" FOREIGN KEY ("recordid") REFERENCES "public"."plugin_deviceinfo_deviceparams" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_deviceinfo_deviceparams_mobile', 'plugin_deviceinfo_deviceparams_mobile_recordid_fkey', 'f', 'ALTER TABLE "public"."plugin_deviceinfo_deviceparams_mobile" ADD CONSTRAINT "plugin_deviceinfo_deviceparams_mobile_recordid_fkey" FOREIGN KEY ("recordid") REFERENCES "public"."plugin_deviceinfo_deviceparams" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_deviceinfo_deviceparams_mobile2', 'plugin_deviceinfo_deviceparams_mobile2_recordid_fkey', 'f', 'ALTER TABLE "public"."plugin_deviceinfo_deviceparams_mobile2" ADD CONSTRAINT "plugin_deviceinfo_deviceparams_mobile2_recordid_fkey" FOREIGN KEY ("recordid") REFERENCES "public"."plugin_deviceinfo_deviceparams" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_deviceinfo_deviceparams_wifi', 'plugin_deviceinfo_deviceparams_wifi_recordid_fkey', 'f', 'ALTER TABLE "public"."plugin_deviceinfo_deviceparams_wifi" ADD CONSTRAINT "plugin_deviceinfo_deviceparams_wifi_recordid_fkey" FOREIGN KEY ("recordid") REFERENCES "public"."plugin_deviceinfo_deviceparams" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_deviceinfo_settings', 'plugin_deviceinfo_settings_customerid_fkey', 'f', 'ALTER TABLE "public"."plugin_deviceinfo_settings" ADD CONSTRAINT "plugin_deviceinfo_settings_customerid_fkey" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_devicelog_log', 'plugin_devicelog_log_applicationid_fkey', 'f', 'ALTER TABLE "public"."plugin_devicelog_log" ADD CONSTRAINT "plugin_devicelog_log_applicationid_fkey" FOREIGN KEY ("applicationid") REFERENCES "public"."applications" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_devicelog_log', 'plugin_devicelog_log_customerid_fkey', 'f', 'ALTER TABLE "public"."plugin_devicelog_log" ADD CONSTRAINT "plugin_devicelog_log_customerid_fkey" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_devicelog_log', 'plugin_devicelog_log_deviceid_fkey', 'f', 'ALTER TABLE "public"."plugin_devicelog_log" ADD CONSTRAINT "plugin_devicelog_log_deviceid_fkey" FOREIGN KEY ("deviceid") REFERENCES "public"."devices" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_devicelog_setting_rule_devices', 'plugin_devicelog_setting_rule_devices_deviceid_fkey', 'f', 'ALTER TABLE "public"."plugin_devicelog_setting_rule_devices" ADD CONSTRAINT "plugin_devicelog_setting_rule_devices_deviceid_fkey" FOREIGN KEY ("deviceid") REFERENCES "public"."devices" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_devicelog_setting_rule_devices', 'plugin_devicelog_setting_rule_devices_ruleid_fkey', 'f', 'ALTER TABLE "public"."plugin_devicelog_setting_rule_devices" ADD CONSTRAINT "plugin_devicelog_setting_rule_devices_ruleid_fkey" FOREIGN KEY ("ruleid") REFERENCES "public"."plugin_devicelog_settings_rules" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_devicelog_settings', 'plugin_devicelog_settings_customerid_fkey', 'f', 'ALTER TABLE "public"."plugin_devicelog_settings" ADD CONSTRAINT "plugin_devicelog_settings_customerid_fkey" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_devicelog_settings_rules', 'plugin_devicelog_settings_rules_applicationid_fkey', 'f', 'ALTER TABLE "public"."plugin_devicelog_settings_rules" ADD CONSTRAINT "plugin_devicelog_settings_rules_applicationid_fkey" FOREIGN KEY ("applicationid") REFERENCES "public"."applications" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_devicelog_settings_rules', 'plugin_devicelog_settings_rules_configurationid_fkey', 'f', 'ALTER TABLE "public"."plugin_devicelog_settings_rules" ADD CONSTRAINT "plugin_devicelog_settings_rules_configurationid_fkey" FOREIGN KEY ("configurationid") REFERENCES "public"."configurations" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_devicelog_settings_rules', 'plugin_devicelog_settings_rules_groupid_fkey', 'f', 'ALTER TABLE "public"."plugin_devicelog_settings_rules" ADD CONSTRAINT "plugin_devicelog_settings_rules_groupid_fkey" FOREIGN KEY ("groupid") REFERENCES "public"."groups" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_devicelog_settings_rules', 'plugin_devicelog_settings_rules_settingid_fkey', 'f', 'ALTER TABLE "public"."plugin_devicelog_settings_rules" ADD CONSTRAINT "plugin_devicelog_settings_rules_settingid_fkey" FOREIGN KEY ("settingid") REFERENCES "public"."plugin_devicelog_settings" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_messaging_messages', 'plugin_messaging_messages_customerid_fkey', 'f', 'ALTER TABLE "public"."plugin_messaging_messages" ADD CONSTRAINT "plugin_messaging_messages_customerid_fkey" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_messaging_messages', 'plugin_messaging_messages_deviceid_fkey', 'f', 'ALTER TABLE "public"."plugin_messaging_messages" ADD CONSTRAINT "plugin_messaging_messages_deviceid_fkey" FOREIGN KEY ("deviceid") REFERENCES "public"."devices" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_push_messages', 'plugin_push_messages_customerid_fkey', 'f', 'ALTER TABLE "public"."plugin_push_messages" ADD CONSTRAINT "plugin_push_messages_customerid_fkey" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_push_messages', 'plugin_push_messages_deviceid_fkey', 'f', 'ALTER TABLE "public"."plugin_push_messages" ADD CONSTRAINT "plugin_push_messages_deviceid_fkey" FOREIGN KEY ("deviceid") REFERENCES "public"."devices" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('plugin_push_schedule', 'plugin_push_schedule_customerid_fkey', 'f', 'ALTER TABLE "public"."plugin_push_schedule" ADD CONSTRAINT "plugin_push_schedule_customerid_fkey" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('pluginsdisabled', 'pluginsdisabled_customerid_fkey', 'f', 'ALTER TABLE "public"."pluginsdisabled" ADD CONSTRAINT "pluginsdisabled_customerid_fkey" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('pluginsdisabled', 'pluginsdisabled_pluginid_fkey', 'f', 'ALTER TABLE "public"."pluginsdisabled" ADD CONSTRAINT "pluginsdisabled_pluginid_fkey" FOREIGN KEY ("pluginid") REFERENCES "public"."plugins" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('pushmessages', 'pushmessages_deviceid_fkey', 'f', 'ALTER TABLE "public"."pushmessages" ADD CONSTRAINT "pushmessages_deviceid_fkey" FOREIGN KEY ("deviceid") REFERENCES "public"."devices" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('settings', 'fk_customer_5', 'f', 'ALTER TABLE "public"."settings" ADD CONSTRAINT "fk_customer_5" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('uploadedfiles', 'uploadedfiles_customerid_fkey', 'f', 'ALTER TABLE "public"."uploadedfiles" ADD CONSTRAINT "uploadedfiles_customerid_fkey" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('userconfigurationaccess', 'userconfigurationaccess_configurationid_fkey', 'f', 'ALTER TABLE "public"."userconfigurationaccess" ADD CONSTRAINT "userconfigurationaccess_configurationid_fkey" FOREIGN KEY ("configurationid") REFERENCES "public"."configurations" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('userconfigurationaccess', 'userconfigurationaccess_userid_fkey', 'f', 'ALTER TABLE "public"."userconfigurationaccess" ADD CONSTRAINT "userconfigurationaccess_userid_fkey" FOREIGN KEY ("userid") REFERENCES "public"."users" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('userdevicegroupsaccess', 'userdevicegroupsaccess_groupid_fkey', 'f', 'ALTER TABLE "public"."userdevicegroupsaccess" ADD CONSTRAINT "userdevicegroupsaccess_groupid_fkey" FOREIGN KEY ("groupid") REFERENCES "public"."groups" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('userdevicegroupsaccess', 'userdevicegroupsaccess_userid_fkey', 'f', 'ALTER TABLE "public"."userdevicegroupsaccess" ADD CONSTRAINT "userdevicegroupsaccess_userid_fkey" FOREIGN KEY ("userid") REFERENCES "public"."users" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('userhints', 'userhints_userid_fkey', 'f', 'ALTER TABLE "public"."userhints" ADD CONSTRAINT "userhints_userid_fkey" FOREIGN KEY ("userid") REFERENCES "public"."users" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('userrolepermissions', 'userrolepermissions_permissionid_fkey', 'f', 'ALTER TABLE "public"."userrolepermissions" ADD CONSTRAINT "userrolepermissions_permissionid_fkey" FOREIGN KEY ("permissionid") REFERENCES "public"."permissions" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('userrolepermissions', 'userrolepermissions_roleid_fkey', 'f', 'ALTER TABLE "public"."userrolepermissions" ADD CONSTRAINT "userrolepermissions_roleid_fkey" FOREIGN KEY ("roleid") REFERENCES "public"."userroles" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('userrolesettings', 'userrolesettings_customerid_fkey', 'f', 'ALTER TABLE "public"."userrolesettings" ADD CONSTRAINT "userrolesettings_customerid_fkey" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('userrolesettings', 'userrolesettings_roleid_fkey', 'f', 'ALTER TABLE "public"."userrolesettings" ADD CONSTRAINT "userrolesettings_roleid_fkey" FOREIGN KEY ("roleid") REFERENCES "public"."userroles" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('users', 'fk_customer_6', 'f', 'ALTER TABLE "public"."users" ADD CONSTRAINT "fk_customer_6" FOREIGN KEY ("customerid") REFERENCES "public"."customers" ("id") ON DELETE CASCADE ON UPDATE NO ACTION;'),
            ('users', 'users_userroleid_fkey', 'f', 'ALTER TABLE "public"."users" ADD CONSTRAINT "users_userroleid_fkey" FOREIGN KEY ("userroleid") REFERENCES "public"."userroles" ("id") ON DELETE RESTRICT ON UPDATE NO ACTION;')
        ) AS t(table_name, constraint_name, constraint_type, constraint_sql)
    LOOP
        IF NOT EXISTS (
            SELECT 1
            FROM pg_constraint
            WHERE conname = rec.constraint_name
        ) THEN
            EXECUTE rec.constraint_sql;
        END IF;
    END LOOP;
EXCEPTION
    WHEN undefined_table THEN
        NULL;
END $$;
