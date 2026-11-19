package utils

// File overview:
// - Responsibility: static Vietnamese bank directory (NAPAS BIN -> bank) used by the /banklist and VietQR utilities.
// - Key symbols: `BankInfo`, `BankInfoByCode`, `GetBankInfo`, `BankList`.
// - Pattern: Read-only lookup table; callers must not mutate the returned map.

type BankInfo struct {
	FullName string
	Abbrev   string
	Code     string
}

var BankInfoByCode = map[string]BankInfo{
	"970425": {"970425", "ABB", "TMCP An Bình"},
	"970416": {"970416", "ACB", "TMCP Á Châu"},
	"970409": {"970409", "BAB", "TMCP Bắc Á"},
	"970418": {"970418", "BIDV", "TMCP Đầu tư và Phát triển Việt Nam"},
	"970438": {"970438", "BVB", "TMCP Bảo Việt"},
	"970444": {"970444", "CBB", "TM TNHH MTV Xây Dựng Việt Nam"},
	"970446": {"970446", "CCF", "Hợp tác xã Việt Nam"},
	"422589": {"422589", "CIMB", "TNHH MTV CIMB"},
	"796500": {"796500", "DBS", "DBS - chi nhánh TP. HCM"},
	"970406": {"970406", "DOB", "TMCP Đông Á"},
	"970431": {"970431", "EIB", "TMCP Xuất nhập khẩu Việt Nam"},
	"970408": {"970408", "GPB", "TM TNHH MTV Dầu Khí Toàn Cầu"},
	"970437": {"970437", "HDB", "TMCP Phát Triển TP Hồ Chí Minh"},
	"970442": {"970442", "HLBVN", "TNHH MTV Hongleong Việt Nam"},
	"458761": {"458761", "HSBC", "Ngân hàng TNHH MTV HSBC (VN)"},
	"970455": {"970455", "IBK - HN", "IBK - CN Hà Nội"},
	"970456": {"970456", "IBK - HCM", "IBK - CN HCM"},
	"970415": {"970415", "ICB", "TMCP Công thương Việt Nam"},
	"970434": {"970434", "IVB", "TNHH Indovina"},
	"970462": {"970462", "KBHN", "Kookmin - CN Hà Nội"},
	"970463": {"970463", "KBHCM", "Kookmin - CN TP HCM"},
	"970452": {"970452", "KLB", "TMCP Kiên Long"},
	"963399": {"963399", "UMEE", "UMEE by Kienlongbank"},
	"668888": {"668888", "KBank", "Kasikornbank - CN TP. HCM"},
	"970449": {"970449", "LPB", "TMCP Bưu Điện Liên Việt"},
	"970422": {"970422", "MB", "TMCP Quân Đội"},
	"970426": {"970426", "MSB", "TMCP Hàng Hải Việt Nam"},
	"970428": {"970428", "NAB", "TMCP Nam Á"},
	"970419": {"970419", "NCB", "TMCP Quốc Dân"},
	"801011": {"801011", "NHB - HN", "NONGHYUP - CN Hà Nội"},
	"970448": {"970448", "OCB", "TMCP Phương Đông"},
	"970414": {"970414", "OJB", "TM TNHH MTV Đại Dương"},
	"970453": {"970453", "PBVN", "TNHH MTV Public Việt Nam"},
	"970430": {"970430", "PGB", "TMCP Xăng Dầu Petrolimex"},
	"970412": {"970412", "PVCB", "TMCP Đại Chúng Việt Nam"},
	"970429": {"970429", "SCB", "TMCP Sài Gòn"},
	"970410": {"970410", "SCVN", "TNHH MTV Standard Chartered Việt Nam"},
	"970440": {"970440", "SEAB", "TMCP Đông Nam Á"},
	"970400": {"970400", "SGICB", "TMCP Sài Gòn Công Thương"},
	"970443": {"970443", "SHB", "TMCP Sài Gòn Hà Nội"},
	"970403": {"970403", "STB", "TMCP Sài Gòn Thương Tín"},
	"970424": {"970424", "SHBVN", "TNHH MTV Shinhan Việt Nam"},
	"970407": {"970407", "TCB", "TMCP Kỹ thương Việt Nam"},
	"970423": {"970423", "TPB", "TMCP Tiên Phong"},
	"970458": {"970458", "UOB", "TNHH MTV United Overseas"},
	"970427": {"970427", "VAB", "TMCP Việt Á"},
	"970499": {"970499", "VBARD TTT", "NN & PT Nông thôn VN"},
	"970405": {"970405", "VBARD TTTT", "NN & PT Nông thôn VN"},
	"970436": {"970436", "VCB", "TMCP Ngoại Thương Việt Nam"},
	"970454": {"970454", "VCCB", "TMCP Bản Việt"},
	"970441": {"970441", "VIB", "TMCP Quốc tế Việt Nam"},
	"970433": {"970433", "VIETBANK", "TMCP Việt Nam Thương Tín"},
	"970432": {"970432", "VPB", "TMCP Việt Nam Thịnh Vượng"},
	"546034": {"546034", "CAKE", "Ngân hàng số CAKE by VPBank"},
	"546035": {"546035", "Ubank", "Ngân hàng số Ubank by VPBank"},
	"970421": {"970421", "VRB", "Liên doanh Việt - Nga"},
	"970457": {"970457", "WVN", "TNHH MTV Woori Việt Nam"},
	"999888": {"999888", "VBSP", "Chính sách Xã hội"},
	"970467": {"970467", "KEB Hana HN", "KEB HANA - CN Hà Nội"},
	"970466": {"970466", "KEB Hana HCM", "KEB HANA - CN TP HCM"},
	"971011": {"971011", "VNPTFT", "TT DV Tài chính số VNPT"},
	"971005": {"971005", "VTEL", "DV số Viettel"},
	"963388": {"963388", "TIMO", "TMCP Bản Việt - CN TIMO"},
	"977777": {"977777", "MAFC", "MIRAE ASSET VIETNAM"},
	"963369": {"963369", "LIO", "Ngân hàng số LIOBANK"},
	"971032": {"971032", "MOBI", "DV Số Mobifone"},
	"533948": {"533948", "CITI", "CITI"},
	"555666": {"555666", "BIDC", "Đầu tư & Phát triển Campuchia"},
	"168999": {"168999", "CUB", "CATHAY UNITED BANK - CN HCM"},
	"963688": {"963688", "BOC", "BANK OF CHINA (HONG KONG)"},
	"963666": {"963666", "BNPPHCM", "BNP PARIBAS - CN HCM"},
	"963668": {"963668", "BNPPHN", "BNP PARIBAS - CN HN"},
	"963368": {"963368", "SVFC", "TC TC TNHH MTV Shinhan VN"},
	"963669": {"963669", "ICBC", "ICBC - CN Hà Nội"},
	"971133": {"971133", "PVDB", "TMCP Đại Chúng VN Ngân hàng số"},
}

// GetBankInfo returns the bank information for a given bank code.
// If the bank code does not exist, it returns an empty BankInfo and false.
func GetBankInfo(bankCode string) (BankInfo, bool) {
	bankInfo, exists := BankInfoByCode[bankCode]
	return bankInfo, exists
}

// BankList returns the full bank directory keyed by bank BIN code.
func BankList() map[string]BankInfo {
	return BankInfoByCode
}
