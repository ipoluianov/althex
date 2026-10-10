package forms

import "fmt"

var sr = Strings{
	Error: "Грешка",

	MenuFile:            "Датотека",
	MenuOpen:            "Отвори...",
	MenuSave:            "Сачувај",
	MenuSaveAs:          "Сачувај као...",
	MenuClose:           "Затвори",
	MenuRecent:          "Недавне датотеке",
	MenuExit:            "Излаз",
	MenuEdit:            "Уређивање",
	MenuUndo:            "Опозови",
	MenuRedo:            "Понови",
	MenuCut:             "Исеци",
	MenuCopy:            "Копирај",
	MenuPaste:           "Налепи",
	MenuSelectAll:       "Изабери све",
	MenuInsertMode:      "Режим уметања (Ins)",
	MenuReadOnly:        "Само за читање",
	MenuView:            "Приказ",
	MenuGoTo:            "Иди на померај...",
	MenuZoomIn:          "Увећај",
	MenuZoomOut:         "Умањи",
	MenuZoomReset:       "Нормална величина",
	MenuNew:             "Нови",
	MenuNewMore:         "Нови из",
	MenuNewSize:         "Нула задате величине...",
	MenuNewClipboard:    "Оставе",
	MenuCloseAll:        "Затвори све",
	MenuNavigate:        "Навигација",
	MenuBack:            "Назад",
	MenuForward:         "Напред",
	MenuToggleBookmark:  "Постави или уклони обележивач",
	MenuNextBookmark:    "Следећи обележивач",
	MenuPrevBookmark:    "Претходни обележивач",
	MenuExportBookmarks: "Извези обележиваче...",
	MenuImportBookmarks: "Увези обележиваче...",
	MenuNextTab:         "Следећа картица",
	MenuPrevTab:         "Претходна картица",
	MenuBytesPerRow:     "Бајтова у реду",
	MenuFitWidth:        "Према ширини прозора",
	MenuGroup:           "Груписање бајтова",
	MenuMode:            "Бајтови као",
	MenuOffsets:         "Помераји",
	MenuRelativeOffsets: "Од ознаке",
	MenuSetMark:         "Постави ознаку овде",
	MenuEncoding:        "Кодирање текста",
	MenuSplit:           "Подели приказ",
	MenuSyncScroll:      "Синхроно померање",
	ModeHex:             "Хексадецимални",
	ModeBin:             "Бинарни",
	ModeOct:             "Октални",
	ModeDec:             "Децимални",
	SizeLabel:           "Величина:",
	MenuPanels:          "Панели",
	PageInspector:       "Инспектор података",
	BigEndian:           "Big-endian (старији бајт први)",
	ColType:             "Тип",
	ColValue:            "Вредност",
	PageBookmarks:       "Обележивачи",
	PageHashes:          "Хешеви",
	ColName:             "Име",
	ColOffset:           "Померај",
	ColLength:           "Дужина",
	ColComment:          "Коментар",
	ColAlgorithm:        "Алгоритам",
	Remove:              "Уклони",
	Range:               "Опсег:",
	RangeSelection:      "Избор",
	RangeDocument:       "Цео документ",
	CustomCRC:           "Сопствени CRC:",
	Calculate:           "Израчунај",
	Cancel:              "Откажи",
	StatsLine: func(off, n int64, entropy, seconds float64) string {
		return fmt.Sprintf("%X..%X (бајтова: %d): ентропија %.4f бит/бајт, %.2f с", off, off+n, n, entropy, seconds)
	},
	Working:    func(percent int) string { return fmt.Sprintf("Обрада... %d%%", percent) },
	BytesLabel: "Бајтови (hex или \"текст\"):",
	BytesBad:   "Hex цифре као FF 00 или текст под наводницима",
	BitsLabel:  "Битови (0-7):",
	BitsBad:    "Број битова од 0 до 7",
	MenuFill:   "Попуни...",
	OpAdd:      "Додај...",
	OpSub:      "Одузми...",
	OpShl:      "Помери улево...",
	OpShr:      "Помери удесно...",
	OpRol:      "Ротирај улево...",
	OpRor:      "Ротирај удесно...",
	OpSwap: func(width int) string {
		return fmt.Sprintf("Замени бајтове у групама од %d", width)
	},
	OpReverse:     "Обрни редослед бајтова",
	CopyHex:       "Hex са размацима",
	CopyHexPlain:  "Hex без размака",
	CopyEscaped:   "Ниска са ескејп знацима",
	ImportArray:   "Низ из кода...",
	MenuCopyAs:    "Копирај као",
	MenuBitOps:    "Битовске операције",
	MenuImport:    "Увоз",
	MenuExport:    "Извези избор",
	ImportedAt:    func(base uint64) string { return fmt.Sprintf("Подаци почињу на адреси %X", base) },
	AddressLabel:  "Адреса првог бајта:",
	FillKind:      "Попуни:",
	FillPattern:   "Поновљеним бајтовима",
	FillRandom:    "Насумичним бајтовима",
	FillIncrement: "Растућим бројевима",
	FillValue:     "Бајтови:",
	FillStart:     "Први број:",
	FillStep:      "Корак:",
	FillWidth:     "Величина броја:",
	FillInsert:    "Уметни уместо преписивања",
	MenuPalette:   "Палета команди...",
	ColCommand:    "Команда",
	ColShortcut:   "Тастери",
	PageSearch:    "Претрага",
	MenuSearch:    "Претрага",
	MenuFind:      "Пронађи...",
	MenuReplace:   "Замени...",
	FindHex:       "Hex",
	FindText:      "Текст",
	ReplaceWith:   "Замени са:",
	IgnoreCase:    "Занемари велика и мала слова",
	AllDocuments:  "У свим документима",
	FindNext:      "Пронађи следеће",
	FindPrev:      "Пронађи претходно",
	FindAll:       "Пронађи све",
	Replace:       "Замени",
	ReplaceAll:    "Замени све",
	Strings:       "Ниске",
	Export:        "Извези...",
	ColDocument:   "Документ",
	ColFound:      "Пронађено",
	NothingToFind: "Нема шта да се тражи",
	NotFound:      "Није пронађено",
	Wrapped:       "Претрага је настављена од другог краја",
	ClearMarks:    "Уклони ознаке претраге",
	FoundCount: func(n int, more bool, seconds float64) string {
		s := fmt.Sprintf("Пронађено: %d за %.2f с", n, seconds)
		if more {
			s += " (само први)"
		}
		return s
	},
	ReplacedCount:     func(n int) string { return fmt.Sprintf("Замењено: %d", n) },
	MenuCompareMenu:   "Поређење",
	MenuCompare:       "Упореди са датотеком...",
	MenuCompareShifts: "Упореди са померајима...",
	MenuNextDiff:      "Следећа разлика",
	MenuPrevDiff:      "Претходна разлика",
	MenuCompareReport: "Извези извештај...",
	MenuMakePatch:     "Сачувај измене као закрпу",
	MenuApplyPatch:    "Примени закрпу...",
	CompareSaveFirst:  "Сачувајте документ пре поређења",
	NoMoreDiffs:       "Нема више разлика",
	PatchNoFile:       "Документ нема датотеку од које би се направила закрпа",
	PatchSaved:        "Закрпа је сачувана",
	PatchUnknown:      "Непознат формат закрпе",
	Comparing:         func(percent int) string { return fmt.Sprintf("Поређење... %d%%", percent) },
	CompareSummary: func(ranges int, changed, inserted, deleted int64, seconds float64) string {
		return fmt.Sprintf("Разлике: %d (бајтова измењено: %d, уметнуто: %d, обрисано: %d), %.2f с", ranges, changed, inserted, deleted, seconds)
	},
	DiffStatus:        func(cur, total int) string { return fmt.Sprintf("Разлика %d од %d", cur, total) },
	ColorRules:        "Боје бајтова:",
	ColorRulesHint:    "нпр. 00-1F=#ff8800; 80-FF=#4488ff",
	PageStructure:     "Структура",
	PageConsole:       "Конзола",
	MenuApplyTemplate: "Примени шаблон",
	Apply:             "Примени",
	ColSize:           "Величина",
	RunFunction:       "Покрени",
	AutoTemplate:      "Аутоматски (према формату)",
	NoTemplate:        "Нема шаблона за ову датотеку",
	NoTemplateApplied: "Шаблон није примењен (F5 — примени)",
	ChecksumPrefix:    "Контролни збир: ",
	Evaluate:          "Изврши",
	Clear:             "Очисти",
	ConsoleHint:       "Изрази над документом: ReadUInt(0x3C), Checksum(\"crc32\", 0, FileSize()), поља шаблона по имену",
	NoDocument:        "Нема отвореног документа",
	MoreElements:      func(n int64) string { return fmt.Sprintf("... и још %d", n) },
	TemplateError:     func(name, err string) string { return "Грешка у шаблону " + name + ":\n\n" + err },
	TemplateWrites: func(name string) string {
		return "Шаблон " + name + " уписује у документ. Покренути га?\n\nЊегове измене се могу опозвати одједном."
	},
	TemplateRunning: func(name string) string { return name + ": извршава се..." },
	TemplateDone: func(name string, fields int, seconds float64) string {
		return fmt.Sprintf("%s: поља: %d, %.2f с", name, fields, seconds)
	},
	MenuOpenDisk:      "Отвори диск...",
	PageDisk:          "Диск",
	PageFiles:         "Датотеке",
	PageBoot:          "Покретачка слика",
	PageDisasm:        "Дисасемблер",
	ColModel:          "Модел",
	ColPath:           "Путања",
	ColProblem:        "Проблем",
	ColModified:       "Измењено",
	ColCompression:    "Компресија",
	ColAddress:        "Адреса",
	ColBytes:          "Бајтови",
	ColInstruction:    "Инструкција",
	DiskReadOnlyNote:  "Дискови се отварају само за читање; упис на њих се потврђује двапут",
	DiskWriteTitle:    "Упис на диск",
	DiskWriteEdits:    "дозволити уређивање",
	DiskWriteSave:     "уписати измењене секторе",
	OpenPartition:     "Отвори",
	ShowFiles:         "Датотеке",
	BootCode:          "Покретачки код",
	Refresh:           "Освежи",
	NoPartitionTable:  "Нема табеле партиција",
	ChoosePartition:   "Изаберите партицију",
	NoFileSystemShown: "Изаберите партицију на страници „Диск” и притисните „Датотеке”",
	GoToBytes:         "Иди на бајтове",
	Extract:           "Издвоји...",
	OpenAsDocument:    "Отвори",
	NoExtents:         "Бајтови датотеке нису смештени у непрекидним деловима",
	NoBootImage:       "Није покретачка слика познатог формата",
	LoadAddress:       "адреса",
	FromCaret:         "Од курсора",
	OpenDiskInside: func(name, format string) string {
		return name + " је слика диска (" + format + "). Отворити и диск у њој?"
	},
	DiskNeedsRights: func(path string) string {
		return "Нема приступа " + path + ". За читање дискова потребна су администраторска права: покрените AltHex као администратор (на Linux-у као root или се додајте у групу disk)."
	},
	DiskWriteAsk1: func(path, what string) string {
		return "Радња на диску " + path + ": " + what + ". Грешка може уништити податке целог диска. Наставити?"
	},
	DiskWriteAsk2: func(path string) string {
		return "Да ли сте сигурни? Измене иду директно на " + path + "."
	},
	PartitionInfo: func(scheme string, parts, issues int) string {
		return fmt.Sprintf("%s: партиције: %d, проблеми: %d", scheme, parts, issues)
	},
	NoFileSystem: func(name string) string {
		return "Нема познатог система датотека у " + name
	},
	ExtentsCount:    func(n int) string { return fmt.Sprintf("Делови датотеке: %d", n) },
	Extracted:       func(n int) string { return fmt.Sprintf("Издвојено: %d", n) },
	PageFirmware:    "Фирмвер",
	PageFunctions:   "Функције",
	FirmwareHint:    "Изаберите уређај или притисните „Препознај”; затим „Анализирај”",
	Device:          "Уређај:",
	Architecture:    "Архитектура:",
	NoDevice:        "(без уређаја)",
	Detect:          "Препознај",
	Analyze:         "Анализирај",
	ExportAsm:       "Извези .s...",
	DecodeSelection: "Декодирај избор",
	NothingDetected: "архитектура није препозната",
	Analyzing:       "Анализа...",
	SelectConfig:    "Изаберите бајтове конфигурационе области",
	AnalyzeFirst:    "Прво анализирајте фирмвер",
	Rename:          "Преименуј",
	CommentTitle:    "Коментар",
	ColCallers:      "Позиваоци",
	ColFunction:     "Функција",
	ReferencesTo:    "Референце на ред изабран у листингу:",
	AnalysisDone: func(insts, funcs int) string {
		return fmt.Sprintf("Инструкције: %d, функције: %d. У листингу: N — преименуј, ; — коментар, C — овде је код, Enter — иди", insts, funcs)
	},
	MenuExternal: "Спољни интерфејси...",
	ExtStartFailed: func(err string) string {
		return "Спољни интерфејс се није покренуо: " + err
	},
	ExtAskTitle:      func(client string) string { return "Захтев од " + client },
	ExtUnknownClient: "програма",
	ExtAskOpen:       func(path string) string { return "Отворити датотеку\n" + path + "?" },
	ExtAskSave:       func(name string) string { return "Сачувати " + name + " у датотеку?" },
	ExtAskEdits: func(name, why, list string) string {
		s := "Изменити " + name + ":\n\n" + list
		if why != "" {
			s += "\nРазлог: " + why + "\n"
		}
		return s + "\nИзмене ће бити један корак опозива и не чувају се у датотеку."
	},
	ExtOverwrite: func(n int, off int64) string { return fmt.Sprintf("упиши бајтове (%d) на 0x%X", n, off) },
	ExtInsert: func(n int, off int64) string {
		return fmt.Sprintf("уметни бајтове (%d) на 0x%X", n, off)
	},
	ExtDelete: func(n, off int64) string { return fmt.Sprintf("обриши бајтове (%d) на 0x%X", n, off) },
	ExtMoreEdits: func(n, total int) string {
		return fmt.Sprintf("... и још измена: %d (укупно бајтова: %d)", n, total)
	},
	ExtUndoKind:     func(client string) string { return "измене од " + client },
	ExtApplied:      func(client string) string { return "Измене од " + client + " су примењене" },
	ExtApply:        "Примени",
	ExtDecline:      "Одбиј",
	ExtAPI:          "Локални API (JSON-RPC) за програме и \"althex mcp\"",
	ExtHTTP:         "MCP преко HTTP-а за AI агенте (само 127.0.0.1)",
	ExtPort:         "Порт",
	ExtToken:        "Токен",
	ExtNewToken:     "Нови",
	ExtCopyToken:    "Копирај",
	ExtTrust:        "Примењуј измене клијената без питања (не препоручује се)",
	ExtHint:         "Прихватају се само локалне везе. AI агент може да покрене и \"althex mcp\" (stdio):\nон ради са документима овог прозора када је локални API укључен.\nПреко HTTP-а агент шаље \"Authorization: Bearer <токен>\" на http://127.0.0.1:<порт>/mcp.",
	ExtRunning:      func(where string) string { return "Ради: " + where },
	ExtBadPort:      "Порт је број од 1 до 65535",
	ExtShortToken:   "Токен мора имати најмање 16 знакова",
	Copied:          "Копирано",
	PageHistory:     "Историја",
	ColEdit:         "Измена",
	ColTime:         "Време",
	HistoryGoTo:     "Пређи на ово стање",
	HistoryOriginal: "Као при отварању",
	ColColor:        "Боја",
	BadColor:        "Боја се пише као #rrggbb",
	Canceled:        "Отказано",
	OpenedReadOnly: func(name, reason string) string {
		return "Није могуће уписати " + name + " (" + reason + "). Датотека је отворена само за читање: сачувајте је под другим именом да бисте задржали измене."
	},
	FileChangedAskEdited: func(name string) string {
		return name + " је измењен у другом програму.\n\nДокумент има несачуване измене: поновно учитавање их брише. Поново учитати? „Не” задржава документ са вашим изменама."
	},
	DiskWriteRanges: func(n int, total int64, list string, more int) string {
		s := fmt.Sprintf("\n\nБиће уписано: опсега %d, бајтова %d: %s", n, total, list)
		if more > 0 {
			s += fmt.Sprintf(" и још %d", more)
		}
		return s
	},
	MenuCompareOpen:  "Упореди са отвореним документима...",
	CompareWith:      func(name string) string { return "Упореди " + name + " са:" },
	CompareShiftsOne: "Са померајима (само један документ)",
	CompareAddFiles:  "Додај датотеке...",
	CompareChooseOne: "Изаберите бар један документ",
	CompareTooMany: func(n int) string {
		return fmt.Sprintf("Највише %d докумената се пореди одједном", n)
	},
	CompareSummaryN: func(ranges int, bytes int64, seconds float64) string {
		return fmt.Sprintf("Разлике: опсега %d, бајтова %d, %.2f s", ranges, bytes, seconds)
	},
	MenuMinimap:         "Мини-мапа",
	MemoryMap:           "Мапа меморије уређаја:",
	CallTreeOf:          "Стабло позива изабране функције:",
	CallTreeCallers:     "Позиваоци",
	CallTreeCallees:     "Позване",
	CallCycle:           "(рекурзија)",
	ExportCallGraph:     "Извоз графа позива",
	AddrNotInImage:      "Адреса није у слици",
	OpenDecompressed:    "Отвори распаковано",
	ExtractDecompressed: "Извуци распаковано",
	FindHintHex:         "Бајтови у hex: 4D 5A ?? 50",
	FindHintText:        "Текст за претрагу",
	FindHintNumber:      "Број: 1234, -5, 0x1F, 1.5",
	MinLengthLabel:      "мин.",
	MatchOf: func(i, n int, more bool) string {
		s := fmt.Sprintf("Погодак %d од %d", i, n)
		if more {
			s += "+"
		}
		return s
	},
	GoToHint:     "0x1F, 1F, 1Fh, 31 (декадно), +16, 0x400 + 3*512",
	MenuOptions:  "Опције",
	MenuSettings: "Подешавања...",
	MenuHelp:     "Помоћ",
	MenuDocs:     "Документација",
	MenuAbout:    "О програму",

	DropHint:   "Превуците датотеку овде или је отворите помоћу Ctrl+O",
	OpenFailed: func(path, err string) string { return "Није могуће отворити " + path + ":\n\n" + err },
	Untitled:   "Без имена",

	StatusOffset:   "Померај",
	StatusSelected: "Изабрано",
	StatusValue:    "Вредност",
	StatusSize:     "Величина",
	ModeInsert:     "УМЕ",
	ModeOverwrite:  "ПРЕ",
	ReadOnly:       "Само за читање",
	Saving:         func(percent int) string { return fmt.Sprintf("Чување... %d%%", percent) },
	Saved:          "Сачувано",
	SaveFailed:     func(err string) string { return "Није могуће сачувати датотеку:\n\n" + err },

	SaveChangesTitle: "Чување измена",
	SaveChangesAsk:   func(name string) string { return "Сачувати измене у " + name + "?" },
	Save:             "Сачувај",
	DontSave:         "Не чувај",

	GoToTitle: "Иди на померај",
	GoToLabel: "Померај:",
	GoToBad:   "Иза краја документа",

	RecoverTitle: "Опоравак измена",
	RecoverAsk: func(name, when string) string {
		return "AltHex је затворен са несачуваним изменама у " + name + " (" + when + ").\n\nОпоравити их?"
	},
	RecoverFailed: func(err string) string { return "Није могуће опоравити измене:\n\n" + err },
	FileChangedAsk: func(name string) string {
		return name + " је измењен у другом програму.\n\nПоново учитати? Измене направљене овде биће изгубљене."
	},

	BackupOnSave:   "Чувај резервну копију (.bak) при чувању",
	BookmarkName:   func(n int) string { return fmt.Sprintf("Обележивач %d", n) },
	AlwaysOnTop:    "Увек на врху",
	Language:       "Језик:",
	LanguageSystem: "Као у систему",
	Theme:          "Тема:",
	ThemeDark:      "Тамна",
	ThemeLight:     "Светла",

	AboutTitle:   func(name string) string { return "О програму " + name },
	Version:      "Верзија",
	Author:       "Аутор:",
	License:      "Лиценца:",
	VisitWebsite: "Посети сајт",
	Close:        "Затвори",

	Install:   "Инсталирај",
	Update:    "Ажурирај",
	Uninstall: "Деинсталирај",
	InstallAsk: func(dir string) string {
		return "AltHex ће бити копиран у " + dir + " и додат у мени Старт, на радну површину и у листу инсталираних апликација. Инсталирана копија биће ажурирана. Подешавања остају непромењена.\n\nЗатим ће се AltHex поново покренути одатле."
	},
	InstallFailed: func(err string) string {
		return "Инсталирање програма AltHex није успело:\n\n" + err
	},
	Installed: "AltHex је инсталиран",
	UninstallAsk: func(dir string) string {
		return "Уклонити AltHex са овог рачунара?\n\nПодешавања остају у " + dir + "."
	},
	UninstallRunning: "AltHex је покренут. Затворите га и покушајте поново.",
	Uninstalled:      "AltHex је уклоњен.",
}
