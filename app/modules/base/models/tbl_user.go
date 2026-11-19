package models

// File overview:
// - Responsibility: defines the `base` module model contract for res user, including field metadata, runtime methods, and module-specific invariants.
// - Key symbols: `resUsersSuperuserID`, `resUsersSelfReadableFields`, `resUsersSelfWriteableFields`, `ResUserRecord`.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/i18n"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
	"golang.org/x/crypto/bcrypt"
)

var resUsersSelfReadableFields = map[string]struct{}{
	"signature": {}, "company_id": {}, "login": {}, "email": {}, "name": {}, "image_1920": {},
	"lang_id": {}, "tz": {}, "group_id": {}, "partner_id": {}, "write_date": {}, "share": {}, "phone": {},
}

var resUsersSelfWriteableFields = map[string]struct{}{
	"signature": {}, "company_id": {}, "email": {}, "name": {}, "image_1920": {}, "lang_id": {}, "tz": {}, "phone": {},
}

// ResUserRecord keeps the legacy users table registered under one richer
// base.users-inspired runtime contract without dropping the compatibility
// columns already used by RPC auth and RBAC.
type ResUserRecord struct {
	orm.BaseModel
	PartnerID         *int64     `bun:"partner_id" json:"partner_id" meta:"kind:many2one,model:tbl.partner,ondelete:SET NULL,index,label:Partner ID,help:Links this record to the related tbl.partner record for partner id."`
	Login             string     `bun:"login" json:"login" meta:"index,label:Login,help:Stores the login value for this record."`
	Password          string     `bun:"password" json:"password" meta:"secret,label:Password,help:Stores the password value for this record."`
	Share             bool       `bun:"share" json:"share" meta:"label:Share,help:Controls whether share is enabled for this record."`
	LangID            string     `bun:"lang" json:"lang_id" meta:"kind:many2one,model:tbl.lang,ondelete:SET NULL,index,label:Lang,help:Links this record to the related tbl.lang record while preserving the legacy stored locale code."`
	TZ                string     `bun:"tz" json:"tz" meta:"index,label:Timezone,help:IANA timezone the user works in." selection:"(Africa/Abidjan,Africa/Abidjan),(Africa/Accra,Africa/Accra),(Africa/Addis_Ababa,Africa/Addis Ababa),(Africa/Algiers,Africa/Algiers),(Africa/Asmara,Africa/Asmara),(Africa/Asmera,Africa/Asmera),(Africa/Bamako,Africa/Bamako),(Africa/Bangui,Africa/Bangui),(Africa/Banjul,Africa/Banjul),(Africa/Bissau,Africa/Bissau),(Africa/Blantyre,Africa/Blantyre),(Africa/Brazzaville,Africa/Brazzaville),(Africa/Bujumbura,Africa/Bujumbura),(Africa/Cairo,Africa/Cairo),(Africa/Casablanca,Africa/Casablanca),(Africa/Ceuta,Africa/Ceuta),(Africa/Conakry,Africa/Conakry),(Africa/Dakar,Africa/Dakar),(Africa/Dar_es_Salaam,Africa/Dar es Salaam),(Africa/Djibouti,Africa/Djibouti),(Africa/Douala,Africa/Douala),(Africa/El_Aaiun,Africa/El Aaiun),(Africa/Freetown,Africa/Freetown),(Africa/Gaborone,Africa/Gaborone),(Africa/Harare,Africa/Harare),(Africa/Johannesburg,Africa/Johannesburg),(Africa/Juba,Africa/Juba),(Africa/Kampala,Africa/Kampala),(Africa/Khartoum,Africa/Khartoum),(Africa/Kigali,Africa/Kigali),(Africa/Kinshasa,Africa/Kinshasa),(Africa/Lagos,Africa/Lagos),(Africa/Libreville,Africa/Libreville),(Africa/Lome,Africa/Lome),(Africa/Luanda,Africa/Luanda),(Africa/Lubumbashi,Africa/Lubumbashi),(Africa/Lusaka,Africa/Lusaka),(Africa/Malabo,Africa/Malabo),(Africa/Maputo,Africa/Maputo),(Africa/Maseru,Africa/Maseru),(Africa/Mbabane,Africa/Mbabane),(Africa/Mogadishu,Africa/Mogadishu),(Africa/Monrovia,Africa/Monrovia),(Africa/Nairobi,Africa/Nairobi),(Africa/Ndjamena,Africa/Ndjamena),(Africa/Niamey,Africa/Niamey),(Africa/Nouakchott,Africa/Nouakchott),(Africa/Ouagadougou,Africa/Ouagadougou),(Africa/Porto-Novo,Africa/Porto-Novo),(Africa/Sao_Tome,Africa/Sao Tome),(Africa/Timbuktu,Africa/Timbuktu),(Africa/Tripoli,Africa/Tripoli),(Africa/Tunis,Africa/Tunis),(Africa/Windhoek,Africa/Windhoek),(America/Adak,America/Adak),(America/Anchorage,America/Anchorage),(America/Anguilla,America/Anguilla),(America/Antigua,America/Antigua),(America/Araguaina,America/Araguaina),(America/Argentina/Buenos_Aires,America/Argentina/Buenos Aires),(America/Argentina/Catamarca,America/Argentina/Catamarca),(America/Argentina/ComodRivadavia,America/Argentina/ComodRivadavia),(America/Argentina/Cordoba,America/Argentina/Cordoba),(America/Argentina/Jujuy,America/Argentina/Jujuy),(America/Argentina/La_Rioja,America/Argentina/La Rioja),(America/Argentina/Mendoza,America/Argentina/Mendoza),(America/Argentina/Rio_Gallegos,America/Argentina/Rio Gallegos),(America/Argentina/Salta,America/Argentina/Salta),(America/Argentina/San_Juan,America/Argentina/San Juan),(America/Argentina/San_Luis,America/Argentina/San Luis),(America/Argentina/Tucuman,America/Argentina/Tucuman),(America/Argentina/Ushuaia,America/Argentina/Ushuaia),(America/Aruba,America/Aruba),(America/Asuncion,America/Asuncion),(America/Atikokan,America/Atikokan),(America/Atka,America/Atka),(America/Bahia,America/Bahia),(America/Bahia_Banderas,America/Bahia Banderas),(America/Barbados,America/Barbados),(America/Belem,America/Belem),(America/Belize,America/Belize),(America/Blanc-Sablon,America/Blanc-Sablon),(America/Boa_Vista,America/Boa Vista),(America/Bogota,America/Bogota),(America/Boise,America/Boise),(America/Buenos_Aires,America/Buenos Aires),(America/Cambridge_Bay,America/Cambridge Bay),(America/Campo_Grande,America/Campo Grande),(America/Cancun,America/Cancun),(America/Caracas,America/Caracas),(America/Catamarca,America/Catamarca),(America/Cayenne,America/Cayenne),(America/Cayman,America/Cayman),(America/Chicago,America/Chicago),(America/Chihuahua,America/Chihuahua),(America/Ciudad_Juarez,America/Ciudad Juarez),(America/Coral_Harbour,America/Coral Harbour),(America/Cordoba,America/Cordoba),(America/Costa_Rica,America/Costa Rica),(America/Creston,America/Creston),(America/Cuiaba,America/Cuiaba),(America/Curacao,America/Curacao),(America/Danmarkshavn,America/Danmarkshavn),(America/Dawson,America/Dawson),(America/Dawson_Creek,America/Dawson Creek),(America/Denver,America/Denver),(America/Detroit,America/Detroit),(America/Dominica,America/Dominica),(America/Edmonton,America/Edmonton),(America/Eirunepe,America/Eirunepe),(America/El_Salvador,America/El Salvador),(America/Ensenada,America/Ensenada),(America/Fort_Nelson,America/Fort Nelson),(America/Fort_Wayne,America/Fort Wayne),(America/Fortaleza,America/Fortaleza),(America/Glace_Bay,America/Glace Bay),(America/Godthab,America/Godthab),(America/Goose_Bay,America/Goose Bay),(America/Grand_Turk,America/Grand Turk),(America/Grenada,America/Grenada),(America/Guadeloupe,America/Guadeloupe),(America/Guatemala,America/Guatemala),(America/Guayaquil,America/Guayaquil),(America/Guyana,America/Guyana),(America/Halifax,America/Halifax),(America/Havana,America/Havana),(America/Hermosillo,America/Hermosillo),(America/Indiana/Indianapolis,America/Indiana/Indianapolis),(America/Indiana/Knox,America/Indiana/Knox),(America/Indiana/Marengo,America/Indiana/Marengo),(America/Indiana/Petersburg,America/Indiana/Petersburg),(America/Indiana/Tell_City,America/Indiana/Tell City),(America/Indiana/Vevay,America/Indiana/Vevay),(America/Indiana/Vincennes,America/Indiana/Vincennes),(America/Indiana/Winamac,America/Indiana/Winamac),(America/Indianapolis,America/Indianapolis),(America/Inuvik,America/Inuvik),(America/Iqaluit,America/Iqaluit),(America/Jamaica,America/Jamaica),(America/Jujuy,America/Jujuy),(America/Juneau,America/Juneau),(America/Kentucky/Louisville,America/Kentucky/Louisville),(America/Kentucky/Monticello,America/Kentucky/Monticello),(America/Knox_IN,America/Knox IN),(America/Kralendijk,America/Kralendijk),(America/La_Paz,America/La Paz),(America/Lima,America/Lima),(America/Los_Angeles,America/Los Angeles),(America/Louisville,America/Louisville),(America/Lower_Princes,America/Lower Princes),(America/Maceio,America/Maceio),(America/Managua,America/Managua),(America/Manaus,America/Manaus),(America/Marigot,America/Marigot),(America/Martinique,America/Martinique),(America/Matamoros,America/Matamoros),(America/Mazatlan,America/Mazatlan),(America/Mendoza,America/Mendoza),(America/Menominee,America/Menominee),(America/Merida,America/Merida),(America/Metlakatla,America/Metlakatla),(America/Mexico_City,America/Mexico City),(America/Miquelon,America/Miquelon),(America/Moncton,America/Moncton),(America/Monterrey,America/Monterrey),(America/Montevideo,America/Montevideo),(America/Montreal,America/Montreal),(America/Montserrat,America/Montserrat),(America/Nassau,America/Nassau),(America/New_York,America/New York),(America/Nipigon,America/Nipigon),(America/Nome,America/Nome),(America/Noronha,America/Noronha),(America/North_Dakota/Beulah,America/North Dakota/Beulah),(America/North_Dakota/Center,America/North Dakota/Center),(America/North_Dakota/New_Salem,America/North Dakota/New Salem),(America/Nuuk,America/Nuuk),(America/Ojinaga,America/Ojinaga),(America/Panama,America/Panama),(America/Pangnirtung,America/Pangnirtung),(America/Paramaribo,America/Paramaribo),(America/Phoenix,America/Phoenix),(America/Port-au-Prince,America/Port-au-Prince),(America/Port_of_Spain,America/Port of Spain),(America/Porto_Acre,America/Porto Acre),(America/Porto_Velho,America/Porto Velho),(America/Puerto_Rico,America/Puerto Rico),(America/Punta_Arenas,America/Punta Arenas),(America/Rainy_River,America/Rainy River),(America/Rankin_Inlet,America/Rankin Inlet),(America/Recife,America/Recife),(America/Regina,America/Regina),(America/Resolute,America/Resolute),(America/Rio_Branco,America/Rio Branco),(America/Rosario,America/Rosario),(America/Santa_Isabel,America/Santa Isabel),(America/Santarem,America/Santarem),(America/Santiago,America/Santiago),(America/Santo_Domingo,America/Santo Domingo),(America/Sao_Paulo,America/Sao Paulo),(America/Scoresbysund,America/Scoresbysund),(America/Shiprock,America/Shiprock),(America/Sitka,America/Sitka),(America/St_Barthelemy,America/St Barthelemy),(America/St_Johns,America/St Johns),(America/St_Kitts,America/St Kitts),(America/St_Lucia,America/St Lucia),(America/St_Thomas,America/St Thomas),(America/St_Vincent,America/St Vincent),(America/Swift_Current,America/Swift Current),(America/Tegucigalpa,America/Tegucigalpa),(America/Thule,America/Thule),(America/Thunder_Bay,America/Thunder Bay),(America/Tijuana,America/Tijuana),(America/Toronto,America/Toronto),(America/Tortola,America/Tortola),(America/Vancouver,America/Vancouver),(America/Virgin,America/Virgin),(America/Whitehorse,America/Whitehorse),(America/Winnipeg,America/Winnipeg),(America/Yakutat,America/Yakutat),(America/Yellowknife,America/Yellowknife),(Antarctica/Casey,Antarctica/Casey),(Antarctica/Davis,Antarctica/Davis),(Antarctica/DumontDUrville,Antarctica/DumontDUrville),(Antarctica/Macquarie,Antarctica/Macquarie),(Antarctica/Mawson,Antarctica/Mawson),(Antarctica/McMurdo,Antarctica/McMurdo),(Antarctica/Palmer,Antarctica/Palmer),(Antarctica/Rothera,Antarctica/Rothera),(Antarctica/South_Pole,Antarctica/South Pole),(Antarctica/Syowa,Antarctica/Syowa),(Antarctica/Troll,Antarctica/Troll),(Antarctica/Vostok,Antarctica/Vostok),(Arctic/Longyearbyen,Arctic/Longyearbyen),(Asia/Aden,Asia/Aden),(Asia/Almaty,Asia/Almaty),(Asia/Amman,Asia/Amman),(Asia/Anadyr,Asia/Anadyr),(Asia/Aqtau,Asia/Aqtau),(Asia/Aqtobe,Asia/Aqtobe),(Asia/Ashgabat,Asia/Ashgabat),(Asia/Ashkhabad,Asia/Ashkhabad),(Asia/Atyrau,Asia/Atyrau),(Asia/Baghdad,Asia/Baghdad),(Asia/Bahrain,Asia/Bahrain),(Asia/Baku,Asia/Baku),(Asia/Bangkok,Asia/Bangkok),(Asia/Barnaul,Asia/Barnaul),(Asia/Beirut,Asia/Beirut),(Asia/Bishkek,Asia/Bishkek),(Asia/Brunei,Asia/Brunei),(Asia/Calcutta,Asia/Calcutta),(Asia/Chita,Asia/Chita),(Asia/Choibalsan,Asia/Choibalsan),(Asia/Chongqing,Asia/Chongqing),(Asia/Chungking,Asia/Chungking),(Asia/Colombo,Asia/Colombo),(Asia/Dacca,Asia/Dacca),(Asia/Damascus,Asia/Damascus),(Asia/Dhaka,Asia/Dhaka),(Asia/Dili,Asia/Dili),(Asia/Dubai,Asia/Dubai),(Asia/Dushanbe,Asia/Dushanbe),(Asia/Famagusta,Asia/Famagusta),(Asia/Gaza,Asia/Gaza),(Asia/Harbin,Asia/Harbin),(Asia/Hebron,Asia/Hebron),(Asia/Ho_Chi_Minh,Asia/Ho Chi Minh),(Asia/Hong_Kong,Asia/Hong Kong),(Asia/Hovd,Asia/Hovd),(Asia/Irkutsk,Asia/Irkutsk),(Asia/Istanbul,Asia/Istanbul),(Asia/Jakarta,Asia/Jakarta),(Asia/Jayapura,Asia/Jayapura),(Asia/Jerusalem,Asia/Jerusalem),(Asia/Kabul,Asia/Kabul),(Asia/Kamchatka,Asia/Kamchatka),(Asia/Karachi,Asia/Karachi),(Asia/Kashgar,Asia/Kashgar),(Asia/Kathmandu,Asia/Kathmandu),(Asia/Katmandu,Asia/Katmandu),(Asia/Khandyga,Asia/Khandyga),(Asia/Kolkata,Asia/Kolkata),(Asia/Krasnoyarsk,Asia/Krasnoyarsk),(Asia/Kuala_Lumpur,Asia/Kuala Lumpur),(Asia/Kuching,Asia/Kuching),(Asia/Kuwait,Asia/Kuwait),(Asia/Macao,Asia/Macao),(Asia/Macau,Asia/Macau),(Asia/Magadan,Asia/Magadan),(Asia/Makassar,Asia/Makassar),(Asia/Manila,Asia/Manila),(Asia/Muscat,Asia/Muscat),(Asia/Nicosia,Asia/Nicosia),(Asia/Novokuznetsk,Asia/Novokuznetsk),(Asia/Novosibirsk,Asia/Novosibirsk),(Asia/Omsk,Asia/Omsk),(Asia/Oral,Asia/Oral),(Asia/Phnom_Penh,Asia/Phnom Penh),(Asia/Pontianak,Asia/Pontianak),(Asia/Pyongyang,Asia/Pyongyang),(Asia/Qatar,Asia/Qatar),(Asia/Qostanay,Asia/Qostanay),(Asia/Qyzylorda,Asia/Qyzylorda),(Asia/Rangoon,Asia/Rangoon),(Asia/Riyadh,Asia/Riyadh),(Asia/Saigon,Asia/Saigon),(Asia/Sakhalin,Asia/Sakhalin),(Asia/Samarkand,Asia/Samarkand),(Asia/Seoul,Asia/Seoul),(Asia/Shanghai,Asia/Shanghai),(Asia/Singapore,Asia/Singapore),(Asia/Srednekolymsk,Asia/Srednekolymsk),(Asia/Taipei,Asia/Taipei),(Asia/Tashkent,Asia/Tashkent),(Asia/Tbilisi,Asia/Tbilisi),(Asia/Tehran,Asia/Tehran),(Asia/Tel_Aviv,Asia/Tel Aviv),(Asia/Thimbu,Asia/Thimbu),(Asia/Thimphu,Asia/Thimphu),(Asia/Tokyo,Asia/Tokyo),(Asia/Tomsk,Asia/Tomsk),(Asia/Ujung_Pandang,Asia/Ujung Pandang),(Asia/Ulaanbaatar,Asia/Ulaanbaatar),(Asia/Ulan_Bator,Asia/Ulan Bator),(Asia/Urumqi,Asia/Urumqi),(Asia/Ust-Nera,Asia/Ust-Nera),(Asia/Vientiane,Asia/Vientiane),(Asia/Vladivostok,Asia/Vladivostok),(Asia/Yakutsk,Asia/Yakutsk),(Asia/Yangon,Asia/Yangon),(Asia/Yekaterinburg,Asia/Yekaterinburg),(Asia/Yerevan,Asia/Yerevan),(Atlantic/Azores,Atlantic/Azores),(Atlantic/Bermuda,Atlantic/Bermuda),(Atlantic/Canary,Atlantic/Canary),(Atlantic/Cape_Verde,Atlantic/Cape Verde),(Atlantic/Faeroe,Atlantic/Faeroe),(Atlantic/Faroe,Atlantic/Faroe),(Atlantic/Jan_Mayen,Atlantic/Jan Mayen),(Atlantic/Madeira,Atlantic/Madeira),(Atlantic/Reykjavik,Atlantic/Reykjavik),(Atlantic/South_Georgia,Atlantic/South Georgia),(Atlantic/St_Helena,Atlantic/St Helena),(Atlantic/Stanley,Atlantic/Stanley),(Australia/ACT,Australia/ACT),(Australia/Adelaide,Australia/Adelaide),(Australia/Brisbane,Australia/Brisbane),(Australia/Broken_Hill,Australia/Broken Hill),(Australia/Canberra,Australia/Canberra),(Australia/Currie,Australia/Currie),(Australia/Darwin,Australia/Darwin),(Australia/Eucla,Australia/Eucla),(Australia/Hobart,Australia/Hobart),(Australia/LHI,Australia/LHI),(Australia/Lindeman,Australia/Lindeman),(Australia/Lord_Howe,Australia/Lord Howe),(Australia/Melbourne,Australia/Melbourne),(Australia/NSW,Australia/NSW),(Australia/North,Australia/North),(Australia/Perth,Australia/Perth),(Australia/Queensland,Australia/Queensland),(Australia/South,Australia/South),(Australia/Sydney,Australia/Sydney),(Australia/Tasmania,Australia/Tasmania),(Australia/Victoria,Australia/Victoria),(Australia/West,Australia/West),(Australia/Yancowinna,Australia/Yancowinna),(Europe/Amsterdam,Europe/Amsterdam),(Europe/Andorra,Europe/Andorra),(Europe/Astrakhan,Europe/Astrakhan),(Europe/Athens,Europe/Athens),(Europe/Belfast,Europe/Belfast),(Europe/Belgrade,Europe/Belgrade),(Europe/Berlin,Europe/Berlin),(Europe/Bratislava,Europe/Bratislava),(Europe/Brussels,Europe/Brussels),(Europe/Bucharest,Europe/Bucharest),(Europe/Budapest,Europe/Budapest),(Europe/Busingen,Europe/Busingen),(Europe/Chisinau,Europe/Chisinau),(Europe/Copenhagen,Europe/Copenhagen),(Europe/Dublin,Europe/Dublin),(Europe/Gibraltar,Europe/Gibraltar),(Europe/Guernsey,Europe/Guernsey),(Europe/Helsinki,Europe/Helsinki),(Europe/Isle_of_Man,Europe/Isle of Man),(Europe/Istanbul,Europe/Istanbul),(Europe/Jersey,Europe/Jersey),(Europe/Kaliningrad,Europe/Kaliningrad),(Europe/Kiev,Europe/Kiev),(Europe/Kirov,Europe/Kirov),(Europe/Kyiv,Europe/Kyiv),(Europe/Lisbon,Europe/Lisbon),(Europe/Ljubljana,Europe/Ljubljana),(Europe/London,Europe/London),(Europe/Luxembourg,Europe/Luxembourg),(Europe/Madrid,Europe/Madrid),(Europe/Malta,Europe/Malta),(Europe/Mariehamn,Europe/Mariehamn),(Europe/Minsk,Europe/Minsk),(Europe/Monaco,Europe/Monaco),(Europe/Moscow,Europe/Moscow),(Europe/Nicosia,Europe/Nicosia),(Europe/Oslo,Europe/Oslo),(Europe/Paris,Europe/Paris),(Europe/Podgorica,Europe/Podgorica),(Europe/Prague,Europe/Prague),(Europe/Riga,Europe/Riga),(Europe/Rome,Europe/Rome),(Europe/Samara,Europe/Samara),(Europe/San_Marino,Europe/San Marino),(Europe/Sarajevo,Europe/Sarajevo),(Europe/Saratov,Europe/Saratov),(Europe/Simferopol,Europe/Simferopol),(Europe/Skopje,Europe/Skopje),(Europe/Sofia,Europe/Sofia),(Europe/Stockholm,Europe/Stockholm),(Europe/Tallinn,Europe/Tallinn),(Europe/Tirane,Europe/Tirane),(Europe/Tiraspol,Europe/Tiraspol),(Europe/Ulyanovsk,Europe/Ulyanovsk),(Europe/Uzhgorod,Europe/Uzhgorod),(Europe/Vaduz,Europe/Vaduz),(Europe/Vatican,Europe/Vatican),(Europe/Vienna,Europe/Vienna),(Europe/Vilnius,Europe/Vilnius),(Europe/Volgograd,Europe/Volgograd),(Europe/Warsaw,Europe/Warsaw),(Europe/Zagreb,Europe/Zagreb),(Europe/Zaporozhye,Europe/Zaporozhye),(Europe/Zurich,Europe/Zurich),(Indian/Antananarivo,Indian/Antananarivo),(Indian/Chagos,Indian/Chagos),(Indian/Christmas,Indian/Christmas),(Indian/Cocos,Indian/Cocos),(Indian/Comoro,Indian/Comoro),(Indian/Kerguelen,Indian/Kerguelen),(Indian/Mahe,Indian/Mahe),(Indian/Maldives,Indian/Maldives),(Indian/Mauritius,Indian/Mauritius),(Indian/Mayotte,Indian/Mayotte),(Indian/Reunion,Indian/Reunion),(Pacific/Apia,Pacific/Apia),(Pacific/Auckland,Pacific/Auckland),(Pacific/Bougainville,Pacific/Bougainville),(Pacific/Chatham,Pacific/Chatham),(Pacific/Chuuk,Pacific/Chuuk),(Pacific/Easter,Pacific/Easter),(Pacific/Efate,Pacific/Efate),(Pacific/Enderbury,Pacific/Enderbury),(Pacific/Fakaofo,Pacific/Fakaofo),(Pacific/Fiji,Pacific/Fiji),(Pacific/Funafuti,Pacific/Funafuti),(Pacific/Galapagos,Pacific/Galapagos),(Pacific/Gambier,Pacific/Gambier),(Pacific/Guadalcanal,Pacific/Guadalcanal),(Pacific/Guam,Pacific/Guam),(Pacific/Honolulu,Pacific/Honolulu),(Pacific/Johnston,Pacific/Johnston),(Pacific/Kanton,Pacific/Kanton),(Pacific/Kiritimati,Pacific/Kiritimati),(Pacific/Kosrae,Pacific/Kosrae),(Pacific/Kwajalein,Pacific/Kwajalein),(Pacific/Majuro,Pacific/Majuro),(Pacific/Marquesas,Pacific/Marquesas),(Pacific/Midway,Pacific/Midway),(Pacific/Nauru,Pacific/Nauru),(Pacific/Niue,Pacific/Niue),(Pacific/Norfolk,Pacific/Norfolk),(Pacific/Noumea,Pacific/Noumea),(Pacific/Pago_Pago,Pacific/Pago Pago),(Pacific/Palau,Pacific/Palau),(Pacific/Pitcairn,Pacific/Pitcairn),(Pacific/Pohnpei,Pacific/Pohnpei),(Pacific/Ponape,Pacific/Ponape),(Pacific/Port_Moresby,Pacific/Port Moresby),(Pacific/Rarotonga,Pacific/Rarotonga),(Pacific/Saipan,Pacific/Saipan),(Pacific/Samoa,Pacific/Samoa),(Pacific/Tahiti,Pacific/Tahiti),(Pacific/Tarawa,Pacific/Tarawa),(Pacific/Tongatapu,Pacific/Tongatapu),(Pacific/Truk,Pacific/Truk),(Pacific/Wake,Pacific/Wake),(Pacific/Wallis,Pacific/Wallis),(Pacific/Yap,Pacific/Yap),(UTC,UTC)"`
	Signature         string     `bun:"signature" json:"signature" meta:"label:Signature,help:Stores the signature value for this record."`
	CompanyID         *int64     `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Links this record to the related tbl.company record for company id."`
	CompanyIDs        []int64    `bun:"company_ids,type:jsonb,nullzero" json:"company_ids" meta:"kind:many2many,model:tbl.company,label:Company IDs,help:Lists the related tbl.company records associated with this record."`
	LoginDate         *time.Time `bun:"login_date" json:"login_date" meta:"label:Login Date,help:Stores the date and time for login date."`
	Email             string     `bun:"email" json:"email" meta:"label:Email,help:Stores the email value for this record."`
	Name              string     `bun:"name" json:"name" meta:"label:Name,help:Stores the human readable name for this record."`
	GroupID           int64      `bun:"group_id" json:"group_id" meta:"kind:many2one,model:tbl.groups,index,label:Group ID,help:Links this record to the related tbl.groups record for group id."`
	AllDevicesAllowed bool       `bun:"alldevicesavailable" json:"alldevicesavailable" meta:"label:All Devices Allowed,help:Controls whether all devices allowed is enabled for this record."`
	AllConfigAllowed  bool       `bun:"allconfigavailable" json:"allconfigavailable" meta:"label:All Config Allowed,help:Controls whether all config allowed is enabled for this record."`
	PasswordReset     bool       `bun:"passwordreset" json:"passwordreset" meta:"label:Password Reset,help:Controls whether password reset is enabled for this record."`
	ResetToken        string     `bun:"passwordresettoken" json:"passwordresettoken" meta:"secret,label:Reset Token,help:Stores the reset token value for this record."`
	AuthData          string     `bun:"authdata" json:"authdata" meta:"secret,label:Auth Data,help:Stores the auth data value for this record."`
	TwoFactorSecret   string     `bun:"twofactorsecret" json:"twofactorsecret" meta:"secret,label:Two Factor Secret,help:Stores the two factor secret value for this record."`
	TwoFactorAccepted bool       `bun:"twofactoraccepted" json:"twofactoraccepted" meta:"label:Two Factor Accepted,help:Controls whether two factor accepted is enabled for this record."`
	// APIKey stores the SHA-256 hex digest of the user's X-API-Key (never the key itself); NULL
	// means the user cannot authenticate with X-API-Key. Only honored when app.supportApikey is on.
	APIKey        *string `bun:"api_key,nullzero" json:"api_key" meta:"secret,label:API Key,help:Stores the SHA-256 digest of this user's X-API-Key (use the Generate API Key action to create one); empty disables X-API-Key for this user."`
	LastLoginFail int64   `bun:"lastloginfail" json:"lastloginfail" meta:"label:Last Login Fail,help:Stores the numeric value for last login fail."`
}

var ResUserModelName = "base.users"

var ResUserDefinition = buildResUserDefinition()

var ResUserModelSpec = orm.ModelSpec{
	Name:        ResUserModelName,
	Label:       "Users",
	Description: "RPC user identities authenticated against the legacy users table",
	Definition:  ResUserDefinition,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createResUsers, DescriptionFile: "edc/admin_user_create_api.md", Aliases: []orm.MethodAlias{{Route: "POST /base/admin/users"}}},
		{Name: "write", Public: true, UsesPathID: true, Call: writeResUsers, DescriptionFile: "edc/admin_user_update_api.md", Aliases: []orm.MethodAlias{{Route: "PUT /base/admin/users/:id"}}},
		{Name: "read", Public: true, ReadOnly: true, UsesPathID: true, Call: readResUsers, DescriptionFile: "edc/admin_user_get_api.md", Aliases: []orm.MethodAlias{{Route: "GET /base/admin/users/:id"}}},
		{Name: "search_read", DescriptionFile: "edc/admin_user_list_api.md", Aliases: []orm.MethodAlias{{Route: "GET /base/admin/users"}}},
		{Name: "onchange", Public: true, ModelOnly: true, ReadOnly: true, Call: onchangeResUsers},
		{Name: "on_change_login", Public: true, ReadOnly: true, Call: onChangeLoginResUsers},
		{Name: "name_search", Public: true, ModelOnly: true, ReadOnly: true, Call: nameSearchResUsers},
		{Name: "copy_data", Public: true, ReadOnly: true, Call: copyDataResUsers},
		{Name: "context_get", Public: true, ModelOnly: true, ReadOnly: true, Call: contextGetResUsers},
		{Name: "action_get", Public: true, ModelOnly: true, ReadOnly: true, Call: actionGetResUsers},
		{Name: "authenticate", Public: true, ModelOnly: true, Call: authenticateResUsers},
		// Single password-change entry point (the former model-level change_password was removed):
		// it sets the target record's password through writeResUsers (full write ACL/record rules),
		// for the form button, a user's own row, and bedc "POST /base/admin/users/:id/password".
		{Name: "action_set_password", Public: true, UsesPathID: true, Call: actionSetPasswordResUsers, DescriptionFile: "edc/admin_user_password_reset_api.md",
			Aliases: []orm.MethodAlias{{
				Route: "POST /base/admin/users/:id/password",
				// Doc-only sample body for the Alias API page.
				BodyExample: map[string]any{"password": "N3w-pass-2026"},
				Bind:        bindResUsersPasswordAlias,
				Render:      renderResUsersPasswordAlias,
			}}},
		{Name: "action_generate_api_key", Public: true, UsesPathID: true, Call: actionGenerateAPIKeyResUsers},
		{Name: "preference_save", Public: true, Call: preferenceSaveResUsers},
		{Name: "has_groups", Public: true, ReadOnly: true, Call: hasGroupsResUsers},
		{Name: "has_group", Public: true, ReadOnly: true, Call: hasGroupResUsers},
		{Name: "fields_get", Public: true, ModelOnly: true, ReadOnly: true, Call: fieldsGetResUsers},
		// toggle_active flips `active`, a privileged field, outside the repository write path.
		{Name: "toggle_active", Public: true, UsesPathID: true, Call: toggleActiveResUsers},
	},
}

var ResUserModel = orm.Define[ResUserRecord](&Models, 280, ResUserModelSpec)

// createResUsers creates the res users handled by this package.
func createResUsers(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := normalizeResUsersCreatePayload(firstNonNil(argAt(args, 0), kwargs["values"]))
	if err != nil {
		return nil, err
	}
	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		working := cloneRecordMap(values)
		userValues, partnerValues := splitResUsersDelegatedPartnerValues(ResUserDefinition, ResPartnerDefinition, working)
		// The repository create re-checks the final values; checking here as well stops a refused
		// create before ensureResUsersPartner has created a partner for it.
		if err := ensureResUsersMutationAllowed(ctx, orm.EnvironmentFromContext(ctx), "create", nil, userValues); err != nil {
			return nil, err
		}
		applyResUsersDefaults(ctx, userValues)
		normalizeResUsersAPIKey(userValues)
		if err := ensureResUsersCompanyScopeConsistent(ctx, model, nil, userValues); err != nil {
			return nil, err
		}
		// Keep user.name and the linked partner's display name aligned when the
		// caller omits one side of the relationship during creation.
		if err := syncResUsersCreateNameWithPartner(ctx, userValues, partnerValues); err != nil {
			return nil, err
		}
		if err := ensureResUsersPartner(ctx, userValues, partnerValues); err != nil {
			return nil, err
		}
		if err := normalizeResUsersLang(ctx, userValues); err != nil {
			return nil, err
		}
		generatedPassword, err := applyResUsersGeneratedPassword(userValues)
		if err != nil {
			return nil, err
		}
		if err := normalizeResUsersPassword(userValues); err != nil {
			return nil, err
		}
		// SEC-01: an active login must always carry a stored credential. Persisting
		// an active user with an empty password would create an account that can
		// never authenticate yet looks valid in admin screens, and it weakens the
		// auth path's assumption that every active row has a bcrypt hash.
		if err := requireResUsersActivePassword(userValues); err != nil {
			return nil, err
		}
		// Raw repository create on purpose: CreateRecord would overwrite the user's explicit home
		// company_id with the caller's active company after ensureResUsersCompanyScopeConsistent
		// checked it against company_ids. The T15 override guard already checks an explicit
		// company_id, and the base.users access handler re-checks the final values.
		record, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(ResUserModelName, model), userValues)
		if err != nil {
			return nil, err
		}
		if err := writeResUsersPartnerValues(ctx, record["partner_id"], partnerValues); err != nil {
			return nil, err
		}
		if generatedPassword != "" {
			revealResUsersBootstrapPassword(ctx, userValues, generatedPassword)
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

func writeResUsers(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("base.users.write requires values")
		return nil, orm.InvalidParamsf("base.users.write requires values")
	}
	// Ownership check up front: delegated partner fields (email, phone, ...) never reach the
	// base.users repository write, so its access hook alone would not stop editing another
	// user's partner. Field-level checks still run there on the final values.
	if err := ensureResUsersMutationAllowed(ctx, orm.EnvironmentFromContext(ctx), "write", ids, orm.RecordMap{}); err != nil {
		return nil, err
	}
	userValues, partnerValues := splitResUsersDelegatedPartnerValues(ResUserDefinition, ResPartnerDefinition, values)
	nameSync := planResUsersWriteNameSync(userValues, partnerValues)
	// Treat group_id <= 0 or non-numeric (e.g. false, nil) as "no change" for the
	// primary group column. The frontend sends 0 or false when a many2one field
	// is cleared; writing that value would reset group_id to 0 and break RBAC on
	// the very next request. An intentional assignment always carries a positive
	// group id. The !ok branch catches bool/nil payloads that int64FromAny cannot
	// parse, which would otherwise bypass this guard.
	if gid, ok := int64FromAny(userValues["group_id"]); !ok || gid <= 0 {
		delete(userValues, "group_id")
	}
	if err := normalizeResUsersPassword(userValues); err != nil {
		return nil, err
	}
	normalizeResUsersAPIKey(userValues)
	// Normalize many2one lang_id inputs back into the legacy stored locale code so
	// auth/session code can keep reading a stable string from base_users.lang.
	if err := normalizeResUsersLang(ctx, userValues); err != nil {
		return nil, err
	}
	if err := ensureResUsersCompanyScopeConsistent(ctx, model, ids, userValues); err != nil {
		return nil, err
	}
	if len(userValues) > 0 {
		if _, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(ResUserModelName, model), ids, userValues); err != nil {
			return nil, err
		}
	}
	if err := applyResUsersWriteNameFallback(ctx, ids, userValues, nameSync); err != nil {
		return nil, err
	}
	if len(partnerValues) > 0 {
		if err := writeResUsersPartnerValuesForIDs(ctx, ids, partnerValues); err != nil {
			return nil, err
		}
	}
	return true, nil
}

// readResUsers reads the res users handled by this package.
func readResUsers(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	requestedFields := stringSlice(firstNonNil(kwargs["fields"], argAt(args, 0)))
	userFields, partnerFields, err := splitResUsersReadFields(ResUserDefinition, ResPartnerDefinition, requestedFields)
	if err != nil {
		return nil, err
	}
	readFields := append([]string(nil), userFields...)
	if len(partnerFields) > 0 && !containsString(readFields, "partner_id") {
		readFields = append(readFields, "partner_id")
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResUserModelName, model), ids, readFields)
	if err != nil {
		return nil, err
	}
	if len(partnerFields) == 0 {
		if err := mapResUsersLangCodesToRelationIDs(ctx, rows); err != nil {
			return nil, err
		}
		maskResUsersSecrets(rows)
		return rows, nil
	}
	partnerByUser := make(map[string]int64, len(rows))
	partnerIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		userID := fmt.Sprintf("%d", int64Value(row["id"]))
		partnerID, ok := many2oneID(row["partner_id"])
		if !ok || partnerID <= 0 {
			continue
		}
		partnerByUser[userID] = partnerID
		partnerIDs = append(partnerIDs, fmt.Sprintf("%d", partnerID))
	}
	partnerRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResPartnerModelName, nil), partnerIDs, append([]string{"id"}, partnerFields...))
	if err != nil {
		return nil, err
	}
	partnerLookup := make(map[int64]orm.RecordMap, len(partnerRows))
	for _, row := range partnerRows {
		partnerLookup[int64Value(row["id"])] = row
	}
	for _, row := range rows {
		userID := fmt.Sprintf("%d", int64Value(row["id"]))
		partnerRow, ok := partnerLookup[partnerByUser[userID]]
		if !ok {
			continue
		}
		for _, field := range partnerFields {
			row[field] = partnerRow[field]
		}
	}
	if err := mapResUsersLangCodesToRelationIDs(ctx, rows); err != nil {
		return nil, err
	}
	maskResUsersSecrets(rows)
	return trimResUsersReadRows(rows, requestedFields), nil
}

// maskResUsersSecrets replaces the stored bcrypt password hash with a
// presence marker so standard read/search_read responses never leak the raw
// hash (SRS-F-002 pattern). api_key/mcp_api_key masking was removed along
// with those fields (RBAC/OAuth backend plan §1/§7.4) — OAuth 2.0 bearer
// tokens are self-issued/opaque-refresh and never stored on base_users, so
// there is nothing left on this row for a caller to leak.
func maskResUsersSecrets(rows []orm.RecordMap) {
	for _, row := range rows {
		// Never echo the stored api_key digest; keep only whether one is set.
		if v, ok := row["api_key"]; ok {
			if strings.TrimSpace(stringValue(v)) != "" && v != nil {
				row["api_key"] = "••••••••"
			} else {
				row["api_key"] = nil
			}
		}
		if _, ok := row["password"]; !ok {
			continue
		}
		if strings.TrimSpace(stringValue(row["password"])) != "" {
			row["password"] = resUsersPasswordMask
		} else {
			row["password"] = ""
		}
	}
}

func onchangeResUsers(_ context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	return map[string]any{"value": map[string]any{}}, nil
}

func onChangeLoginResUsers(_ context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	login := strings.TrimSpace(stringValue(values["login"]))
	if login == "" || !strings.Contains(login, "@") {
		return map[string]any{"value": map[string]any{}}, nil
	}
	return map[string]any{"value": map[string]any{"email": login}}, nil
}

func nameSearchResUsers(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	name := firstNonEmpty(strings.TrimSpace(stringArg(args, 0)), strings.TrimSpace(stringValue(kwargs["name"])))
	db, err := runtimeDBForModel(ctx)
	if err != nil {
		return nil, err
	}
	rows := []ResUserRecord{}
	query := db.NewSelect().
		Model(&rows).
		Column("id", "login", "name")
	if name != "" {
		query = query.WhereGroup(" OR ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("login = ?", name).WhereOr("login ILIKE ?", "%"+name+"%").WhereOr("name ILIKE ?", "%"+name+"%")
		})
	}
	if err := query.OrderExpr("login ASC").Limit(100).Scan(ctx); err != nil {
		return nil, err
	}
	out := make([][2]any, 0, len(rows))
	for _, row := range rows {
		label := firstNonEmpty(strings.TrimSpace(row.Login), strings.TrimSpace(row.Name))
		out = append(out, [2]any{row.ID, label})
	}
	return out, nil
}

// copyDataResUsers copies the data res users handled by this package.
func copyDataResUsers(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResUserModelName, nil), ids, nil)
	if err != nil {
		return nil, err
	}
	defaults, err := recordMapFromArgs(args, kwargs, "default", "defaults")
	if err != nil {
		return nil, err
	}
	out := make([]orm.RecordMap, 0, len(rows))
	for _, row := range rows {
		cloned := cloneRecordMap(row)
		delete(cloned, "id")
		delete(cloned, "created_at")
		delete(cloned, "updated_at")
		for key, value := range defaults {
			cloned[key] = value
		}
		if _, exists := defaults["login"]; !exists {
			cloned["login"] = strings.TrimSpace(stringValue(row["login"])) + " (copy)"
		}
		out = append(out, cloned)
	}
	return out, nil
}

func contextGetResUsers(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil || env.UserID() <= 0 {
		return map[string]any{}, nil
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResUserModelName, nil), []string{fmt.Sprintf("%d", env.UserID())}, []string{"lang_id", "tz", "company_id", "company_ids"})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return map[string]any{}, nil
	}
	companyID, _ := many2oneID(rows[0]["company_id"])
	return map[string]any{
		"lang":                userLanguage(ctx, env.UserID(), strings.TrimSpace(stringValue(rows[0]["lang_id"]))),
		"tz":                  strings.TrimSpace(stringValue(rows[0]["tz"])),
		"timezone":            userTimezone(ctx, env.UserID()),
		"uid":                 env.UserID(),
		"allowed_company_ids": int64SliceFromAny(rows[0]["company_ids"]),
		"company_id":          companyID,
	}, nil
}

// userLanguage returns the user's UI language: the stored lang_id when it is offered by
// app.language, otherwise the language of their address country ("VN" -> vi_vn, also only when
// offered), then the stored code, then en_us. Literal table names for the same init-cycle
// reason as userTimezone.
func userLanguage(ctx context.Context, userID int64, stored string) string {
	stored = i18n.NormalizeLang(stored)
	allowed := config.Get().AppLanguages()
	if stored != "" && slices.Contains(allowed, stored) {
		return stored
	}
	fallback := firstNonEmpty(stored, "en_us")
	db := orm.BunDBFromContext(ctx)
	if db == nil || userID <= 0 {
		return fallback
	}
	var country string
	err := db.NewSelect().
		TableExpr("base_users AS u").
		Join("JOIN tbl_partner AS p ON p.id = u.partner_id").
		Join("JOIN tbl_country AS c ON c.id = p.country_id").
		ColumnExpr("c.code").
		Where("u.id = ?", userID).
		Limit(1).
		Scan(ctx, &country)
	if err != nil {
		return fallback
	}
	lang := i18n.LangForCountry(country)
	if lang != "" && slices.Contains(allowed, lang) {
		return lang
	}
	return fallback
}

// userTimezone returns the IANA display timezone of the user's address ("" when unknown): the
// partner's state timezone wins (multi-zone countries), then the country's. Stored times stay UTC;
// the SPA only uses this to display and enter datetimes. A lookup failure just loses the display
// zone, so it is swallowed instead of failing context_get. Literal table names are used because
// the model specs reference this file's init (using them would be an init cycle).
func userTimezone(ctx context.Context, userID int64) string {
	db := orm.BunDBFromContext(ctx)
	if db == nil || userID <= 0 {
		return ""
	}
	var zone string
	err := db.NewSelect().
		TableExpr("base_users AS u").
		Join("JOIN tbl_partner AS p ON p.id = u.partner_id").
		Join("LEFT JOIN tbl_country_state AS s ON s.id = p.state_id").
		Join("LEFT JOIN tbl_country AS c ON c.id = p.country_id").
		ColumnExpr("COALESCE(NULLIF(s.tz, ''), NULLIF(c.tz, ''), '')").
		Where("u.id = ?", userID).
		Limit(1).
		Scan(ctx, &zone)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(zone)
}

func actionGetResUsers(_ context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	return map[string]any{
		"type":      "ui.actions.window",
		"name":      "My Profile",
		"sys_model": ResUserModelName,
		"view_mode": "form",
		"target":    "current",
	}, nil
}

func authenticateResUsers(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	login := firstNonEmpty(strings.TrimSpace(stringArg(args, 0)), strings.TrimSpace(stringValue(kwargs["login"])))
	password := firstNonEmpty(strings.TrimSpace(stringArg(args, 1)), strings.TrimSpace(stringValue(kwargs["password"])))
	user, ok, err := findResUserByLogin(ctx, login)
	if err != nil {
		return nil, err
	}
	if !ok || !user.Active || user.Password == "" {
		// Refusals that skip the real compare cost the same bcrypt time (plan T36).
		orm.EqualizePasswordCheckTiming(password)
		return false, nil
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return false, nil
	}
	return user.ID, nil
}

func preferenceSaveResUsers(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("base.users.preference_save requires values")
		return nil, orm.InvalidParamsf("base.users.preference_save requires values")
	}
	if len(ids) == 0 {
		env := orm.EnvironmentFromContext(ctx)
		if env != nil && env.UserID() > 0 {
			ids = []string{fmt.Sprintf("%d", env.UserID())}
		}
	}
	allowed := make(orm.RecordMap)
	for field, value := range values {
		if _, ok := resUsersSelfWriteableFields[field]; ok {
			allowed[field] = value
		}
	}
	return writeResUsers(ctx, nil, ids, []any{allowed}, nil)
}

func fieldsGetResUsers(_ context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	fields := make(map[string]any)
	for _, field := range ResUserDefinition.Fields {
		entry := map[string]any{
			"type":     field.Kind,
			"string":   field.Label,
			"readonly": field.ReadOnly,
			"required": field.Required,
		}
		// Include the relation model name so the frontend can render a combobox
		// for many2one/many2many fields instead of falling back to a readonly span.
		if field.Relation != "" {
			entry["relation"] = field.Relation
		}
		// Selection options (e.g. the tz dropdown) must reach the form renderer.
		if len(field.Selection) > 0 {
			entry["selection"] = field.Selection
		}
		if _, ok := resUsersSelfReadableFields[field.Name]; ok {
			entry["self_readable"] = true
		}
		if _, ok := resUsersSelfWriteableFields[field.Name]; ok {
			entry["self_writeable"] = true
		}
		fields[field.Name] = entry
	}
	return fields, nil
}

func applyResUsersDefaults(ctx context.Context, values map[string]any) {
	if _, exists := values["active"]; !exists {
		values["active"] = true
	}
	if _, exists := values["share"]; !exists {
		values["share"] = false
	}
	if _, exists := values["lang_id"]; !exists {
		values["lang_id"] = "en_us"
	}
	if _, exists := values["tz"]; !exists {
		values["tz"] = "UTC"
	}
	if _, exists := values["company_id"]; !exists {
		if env := orm.EnvironmentFromContext(ctx); env != nil {
			if companyID, err := env.CompanyID(); err == nil && companyID > 0 {
				values["company_id"] = companyID
			}
		}
	}
	if _, exists := values["company_ids"]; !exists {
		if env := orm.EnvironmentFromContext(ctx); env != nil {
			if companyIDs, err := env.AllowedCompanyIDs(); err == nil && len(companyIDs) > 0 {
				values["company_ids"] = companyIDs
			}
		}
	}
}

// ensureResUsersCompanyScopeConsistent guards the invariant that
// orm.Environment.CompanyID()/AllowedCompanyIDs() rely on: a user's active
// company_id must always be a member of their own company_ids. Letting a
// write leave those two fields out of sync doesn't fail here — it fails
// later, as a confusing "access to unauthorized company N" error on every
// subsequent RPC call the affected user makes, since the ORM's per-request
// environment is seeded straight from these two columns (see
// orm.EnvironmentForIdentity). Reject the inconsistency at write time instead.
func ensureResUsersCompanyScopeConsistent(ctx context.Context, model *orm.ModelMetadata, ids []string, userValues map[string]any) error {
	companyIDRaw, hasCompanyID := userValues["company_id"]
	companyIDsRaw, hasCompanyIDs := userValues["company_ids"]
	if !hasCompanyID && !hasCompanyIDs {
		return nil
	}
	requestedCompanyID := int64Value(companyIDRaw)
	if hasCompanyID && requestedCompanyID <= 0 {
		// Clearing company_id entirely leaves no active company to validate.
		return nil
	}
	if hasCompanyIDs {
		companyIDs := int64SliceFromAny(companyIDsRaw)
		if hasCompanyID {
			if !slices.Contains(companyIDs, requestedCompanyID) {
				log.Errorf("base.users: company_id %d must be included in company_ids", requestedCompanyID)
				return orm.InvalidParamsf("base.users: company_id %d must be included in company_ids", requestedCompanyID)
			}
			return nil
		}
		// Only company_ids is changing (create has no ids yet; nothing stored to
		// compare against, so there is no existing company_id to protect).
		if len(ids) == 0 {
			return nil
		}
		rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResUserModelName, model), ids, []string{"company_id"})
		if err != nil {
			return err
		}
		for _, row := range rows {
			currentCompanyID, _ := many2oneID(row["company_id"])
			if currentCompanyID > 0 && !slices.Contains(companyIDs, currentCompanyID) {
				log.Errorf("base.users: company_ids must still include the active company_id %d", currentCompanyID)
				return orm.InvalidParamsf("base.users: company_ids must still include the active company_id %d", currentCompanyID)
			}
		}
		return nil
	}
	// Only company_id is changing (create already had defaults populate
	// company_ids alongside it, so reaching here means a write on existing ids).
	if len(ids) == 0 {
		return nil
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResUserModelName, model), ids, []string{"company_ids"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		companyIDs := int64SliceFromAny(row["company_ids"])
		if !slices.Contains(companyIDs, requestedCompanyID) {
			log.Errorf("base.users: company_id %d must be included in company_ids", requestedCompanyID)
			return orm.InvalidParamsf("base.users: company_id %d must be included in company_ids", requestedCompanyID)
		}
	}
	return nil
}

func normalizeResUsersCreatePayload(raw any) ([]orm.RecordMap, error) {
	if raw == nil {
		log.Errorf("base.users.create requires values")
		return nil, orm.InvalidParamsf("base.users.create requires values")
	}
	if record, err := ormAnyToRecordMap(raw); err == nil && record != nil {
		return []orm.RecordMap{record}, nil
	}
	switch typed := raw.(type) {
	case []any:
		out := make([]orm.RecordMap, 0, len(typed))
		for _, item := range typed {
			record, err := ormAnyToRecordMap(item)
			if err != nil {
				return nil, err
			}
			out = append(out, record)
		}
		return out, nil
	default:
		log.Errorf("base.users.create values must be an object or list of objects")
		return nil, orm.InvalidParamsf("base.users.create values must be an object or list of objects")
	}
}

// normalizeResUsersLang converts the API-facing many2one value into the legacy
// stored language code so older auth/session flows do not need a schema change.
func normalizeResUsersLang(ctx context.Context, values map[string]any) error {
	raw, exists := values["lang_id"]
	if !exists {
		return nil
	}
	code, err := resolveResUsersLangCode(ctx, raw)
	if err != nil {
		return err
	}
	values["lang_id"] = code
	return nil
}

// resolveResUsersLangCode accepts many2one-style ids/tuples from RPC callers
// and resolves them back to a locale code for the legacy base_users.lang
// column. Plain code strings remain valid for backward compatibility.
func resolveResUsersLangCode(ctx context.Context, raw any) (string, error) {
	switch typed := raw.(type) {
	case nil:
		return "", nil
	case bool:
		if !typed {
			return "", nil
		}
	case []any:
		if len(typed) > 0 {
			if id, ok := int64FromAny(typed[0]); ok && id > 0 {
				row, err := loadSingletonResLangRow(ctx, fmt.Sprintf("%d", id))
				if err != nil {
					return "", err
				}
				return strings.TrimSpace(row.Code), nil
			}
		}
	}

	if id, ok := int64FromAny(raw); ok && id > 0 {
		row, err := loadSingletonResLangRow(ctx, fmt.Sprintf("%d", id))
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(row.Code), nil
	}

	return strings.TrimSpace(stringValue(raw)), nil
}

func splitResUsersReadFields(userDef, partnerDef *orm.SchemaDefinition, fields []string) ([]string, []string, error) {
	if len(fields) == 0 {
		userFields := make([]string, 0, len(userDef.Fields))
		partnerFields := make([]string, 0)
		for _, field := range userDef.Fields {
			userFields = append(userFields, field.Name)
		}
		// Walk each string{"image 1920", "phone", "mobile", "name", "email" entry in order so validation and side effects stay scoped to the current element.
		for _, fieldName := range []string{"image_1920", "phone", "mobile", "name", "email"} {
			if _, err := partnerDef.Field(fieldName); err == nil {
				partnerFields = append(partnerFields, fieldName)
			}
		}
		return userFields, partnerFields, nil
	}
	userFields := make([]string, 0, len(fields))
	partnerFields := make([]string, 0, len(fields))
	for _, field := range fields {
		// Ignore the removed base.users.lang alias entirely so read callers do
		// not leak it into delegated partner field selection.
		if field == "lang" {
			continue
		}
		// Read-field splitting only needs membership checks; avoid logging for
		// removed optional fields such as the old base.users.lang alias.
		if schemaHasField(userDef, field) {
			userFields = append(userFields, field)
			continue
		}
		if schemaHasField(partnerDef, field) {
			partnerFields = append(partnerFields, field)
			continue
		}
	}
	return userFields, partnerFields, nil
}

func schemaHasField(definition *orm.SchemaDefinition, name string) bool {
	if definition == nil {
		return false
	}
	_, ok := definition.Fields[name]
	return ok
}

// mapResUsersLangCodesToRelationIDs exposes the API-facing many2one value while
// the legacy storage still persists the locale code in base_users.lang.
func mapResUsersLangCodesToRelationIDs(ctx context.Context, rows []orm.RecordMap) error {
	if len(rows) == 0 {
		return nil
	}
	codes := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		code := strings.TrimSpace(stringValue(row["lang_id"]))
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	if len(codes) == 0 {
		for _, row := range rows {
			if _, exists := row["lang_id"]; exists {
				row["lang_id"] = nil
			}
		}
		return nil
	}

	// Stored codes may predate canonical lowercase ("en_US"); match case-insensitively.
	lowerCodes := make([]string, len(codes))
	for i, code := range codes {
		lowerCodes[i] = strings.ToLower(code)
	}
	langRows := []ResLangRecord{}
	// Reuse the tbl.lang storage-bound query helper so language lookups keep
	// honoring the registered tbl_lang table even from the base.users package.
	if err := resLangSelectQuery(ctx, &langRows).
		Column("id", "code", "name").
		Where("lower(code) IN (?)", bun.In(idsToAny(lowerCodes))).
		Scan(ctx); err != nil {
		return err
	}

	codeToID := make(map[string][2]any, len(langRows))
	for _, row := range langRows {
		codeToID[strings.ToLower(strings.TrimSpace(row.Code))] = [2]any{row.ID, row.Name}
	}
	for _, row := range rows {
		code := strings.TrimSpace(stringValue(row["lang_id"]))
		if code == "" {
			row["lang_id"] = nil
			continue
		}
		// [id, name] so the many2one widget shows the language name, not the bare id.
		if pair, ok := codeToID[strings.ToLower(code)]; ok {
			row["lang_id"] = pair
			continue
		}
		row["lang_id"] = nil
	}
	return nil
}

// trimResUsersReadRows trims the res users read rows handled by this package.
func trimResUsersReadRows(rows []orm.RecordMap, fields []string) []orm.RecordMap {
	if len(fields) == 0 {
		return rows
	}
	fieldSet := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		fieldSet[field] = struct{}{}
	}
	out := make([]orm.RecordMap, 0, len(rows))
	for _, row := range rows {
		trimmed := make(orm.RecordMap)
		for field := range fieldSet {
			if value, ok := row[field]; ok {
				trimmed[field] = value
			}
		}
		if id, ok := row["id"]; ok {
			trimmed["id"] = id
		}
		out = append(out, trimmed)
	}
	return out
}

// findResUserByLogin finds the res user by login handled by this package.
func findResUserByLogin(ctx context.Context, login string) (ResUserRecord, bool, error) {
	db, err := runtimeDBForModel(ctx)
	if err != nil {
		return ResUserRecord{}, false, err
	}
	rows := []ResUserRecord{}
	if err := db.NewSelect().
		Model(&rows).
		Where("login = ?", login).
		Limit(1).
		Scan(ctx); err != nil {
		return ResUserRecord{}, false, err
	}
	if len(rows) == 0 {
		return ResUserRecord{}, false, nil
	}
	return rows[0], true, nil
}

func int64SliceFromAny(raw any) []int64 {
	switch typed := raw.(type) {
	case []int64:
		return append([]int64(nil), typed...)
	case []any:
		out := make([]int64, 0, len(typed))
		for _, item := range typed {
			// The form widget sends many2many values as ORM commands, e.g.
			// [[6,0,[1,2]]] (replace) or [4,id] (link); flatten them to ids.
			if cmd, ok := item.([]any); ok {
				out = append(out, int64SliceFromCommand(cmd)...)
				continue
			}
			if value, ok := int64FromAny(item); ok {
				out = append(out, value)
			}
		}
		return out
	case []string:
		// masterdata.Service.resolveReferenceList always resolves many2many
		// external-id arrays (e.g. company_ids: ["company.main"]) to []string
		// of numeric ids, never []any, so this case must parse each element
		// the same way []any does above or seed data silently loses every id.
		out := make([]int64, 0, len(typed))
		for _, item := range typed {
			if value, ok := int64FromAny(item); ok {
				out = append(out, value)
			}
		}
		return out
	default:
		if value, ok := int64FromAny(raw); ok && value > 0 {
			return []int64{value}
		}
		return nil
	}
}

// int64SliceFromCommand extracts the target ids of one ORM x2many command
// tuple: [6,0,ids] (replace) and [4,id] (link) add ids; others add none.
func int64SliceFromCommand(cmd []any) []int64 {
	if len(cmd) < 2 {
		return nil
	}
	op, ok := int64FromAny(cmd[0])
	if !ok {
		return nil
	}
	switch op {
	case 6:
		if len(cmd) >= 3 {
			return int64SliceFromAny(cmd[2])
		}
	case 4:
		if id, ok := int64FromAny(cmd[1]); ok {
			return []int64{id}
		}
	}
	return nil
}

// errNoRuntimeDB reports a raw base query with no tenant DB bound to its context.
var errNoRuntimeDB = errors.New("base: no runtime database bound to the request context")

// runtimeDBForModel returns the DB handle bound to the request environment: the open
// transaction when there is one (so reads see the caller's own writes) or the tenant's pool.
// It never falls back to the default DB: on a multi-database node that would read another
// tenant's users and groups (login lookup, has_group, the superadmin check).
func runtimeDBForModel(ctx context.Context) (bun.IDB, error) {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		log.Errorf("%v", errNoRuntimeDB)
		return nil, errNoRuntimeDB
	}
	switch db := env.DB().(type) {
	case nil:
	case *bun.DB:
		if db != nil {
			return db, nil
		}
	case *bun.Tx:
		if db != nil {
			return db, nil
		}
	default:
		return db, nil
	}
	// An environment carrying only a database name resolves it without creating it: the
	// name was validated when the request was bound, so a missing DB is an error here.
	if name := strings.TrimSpace(env.DBName()); name != "" {
		named, err := orm.EnsureNamedBunDBNoCreate(name)
		if err != nil {
			return nil, fmt.Errorf("base: resolve runtime database %q: %w", name, err)
		}
		return named.DB, nil
	}
	log.Errorf("%v", errNoRuntimeDB)
	return nil, errNoRuntimeDB
}
func buildResUserDefinition() *orm.SchemaDefinition {
	definition := orm.ModelDefinition(ResUserModelName, (*ResUserRecord)(nil))
	renameResUsersLangField(definition)
	return definition
}

// renameResUsersLangField keeps the runtime/API contract on lang_id while the
// legacy storage column remains named lang in base_users.
func renameResUsersLangField(definition *orm.SchemaDefinition) {
	if definition == nil {
		return
	}
	field, ok := definition.Fields["lang"]
	if !ok || field == nil {
		return
	}
	delete(definition.Fields, "lang")
	field.Name = "lang_id"
	field.Column = "lang"
	definition.Fields[field.Name] = field
}
