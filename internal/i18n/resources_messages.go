package i18n

func init() {
	for key, value := range map[string]string{
		"resources_title":          "Resources",
		"resources_guide":          "Choose CPU and RAM for this site. Capacity is checked again when you save.",
		"resources_saved":          "Site resources updated.",
		"capacity_unavailable":     "Capacity could not be checked. Check Docker and the worker, then reload.",
		"capacity_title":           "Server capacity",
		"capacity_hint":            "Capacity available for this site after other sites and a host reserve. Stopped sites still count. Docker Desktop reports its VM capacity.",
		"capacity_ram":             "Available RAM",
		"capacity_cpu":             "Available CPU",
		"capacity_total":           "Total",
		"capacity_disk":            "Free disk space",
		"capacity_disk_hint":       "At least 2 GiB must remain free.",
		"plan_custom":              "Custom",
		"resource_memory":          "RAM per container (MiB)",
		"resource_cpu":             "CPU per container",
		"resources_per_container":  "Each limit applies separately to WordPress and MariaDB. A site needs twice the selected RAM and CPU allocation.",
		"resources_change_warning": "Applying changes briefly stops and recreates the two service containers with their existing data. A stopped site stays stopped.",
		"resources_save":           "Save resources",
		"resources_repair_first":   "Refresh or repair the site status before changing resources.",
	} {
		dictionaries["en"][key] = value
	}
	for key, value := range map[string]string{
		"resources_title":          "منابع",
		"resources_guide":          "CPU و حافظهٔ سایت را انتخاب کنید. ظرفیت هنگام ذخیره دوباره بررسی می‌شود.",
		"resources_saved":          "منابع سایت به‌روز شد.",
		"capacity_unavailable":     "بررسی ظرفیت ممکن نشد. Docker و worker را بررسی و صفحه را تازه کنید.",
		"capacity_title":           "ظرفیت سرور",
		"capacity_hint":            "ظرفیت قابل تخصیص به این سایت پس از کسر سهم سایر سایت‌ها و ذخیرهٔ میزبان. سایت‌های متوقف هم محاسبه می‌شوند. در Docker Desktop ظرفیت ماشین مجازی نمایش داده می‌شود.",
		"capacity_ram":             "حافظهٔ قابل تخصیص",
		"capacity_cpu":             "CPU قابل تخصیص",
		"capacity_total":           "کل",
		"capacity_disk":            "فضای آزاد دیسک",
		"capacity_disk_hint":       "حداقل ۲ گیبی‌بایت فضای آزاد لازم است.",
		"plan_custom":              "سفارشی",
		"resource_memory":          "حافظهٔ هر کانتینر (MiB)",
		"resource_cpu":             "CPU هر کانتینر",
		"resources_per_container":  "هر محدودیت جداگانه بر وردپرس و MariaDB اعمال می‌شود. کل سهم سایت دو برابر حافظه و CPU انتخاب‌شده است.",
		"resources_change_warning": "اعمال تغییر، دو کانتینر سرویس را برای مدت کوتاهی متوقف و با داده‌های فعلی بازسازی می‌کند. سایت متوقف، متوقف می‌ماند.",
		"resources_save":           "ذخیرهٔ منابع",
		"resources_repair_first":   "پیش از تغییر منابع، وضعیت سایت را تازه یا مشکل سرویس را برطرف کنید.",
	} {
		dictionaries["fa"][key] = value
	}
	for key, value := range map[string]string{
		"resources_title":          "الموارد",
		"resources_guide":          "اختر المعالج والذاكرة لهذا الموقع. تُفحص السعة مجددًا عند الحفظ.",
		"resources_saved":          "تم تحديث موارد الموقع.",
		"capacity_unavailable":     "تعذر فحص السعة. تحقق من Docker والعامل ثم أعد تحميل الصفحة.",
		"capacity_title":           "سعة الخادم",
		"capacity_hint":            "السعة المتاحة لهذا الموقع بعد خصم المواقع الأخرى واحتياطي المضيف. تُحتسب المواقع المتوقفة أيضًا. يعرض Docker Desktop سعة الآلة الافتراضية.",
		"capacity_ram":             "الذاكرة المتاحة",
		"capacity_cpu":             "المعالج المتاح",
		"capacity_total":           "الإجمالي",
		"capacity_disk":            "مساحة القرص الحرة",
		"capacity_disk_hint":       "يلزم توفر 2 GiB على الأقل.",
		"plan_custom":              "مخصص",
		"resource_memory":          "ذاكرة كل حاوية (MiB)",
		"resource_cpu":             "المعالج لكل حاوية",
		"resources_per_container":  "يُطبق كل حد منفصلًا على WordPress وMariaDB. يحتاج الموقع إلى ضعف تخصيص الذاكرة والمعالج المختار.",
		"resources_change_warning": "يوقف التطبيق حاويتي الخدمة مؤقتًا ويعيد إنشاءهما مع بياناتهما الحالية. يبقى الموقع المتوقف متوقفًا.",
		"resources_save":           "حفظ الموارد",
		"resources_repair_first":   "حدّث حالة الموقع أو أصلح خدماته قبل تغيير الموارد.",
	} {
		dictionaries["ar"][key] = value
	}
	for key, value := range map[string]string{
		"resources_title":          "Recursos",
		"resources_guide":          "Elige CPU y RAM. La capacidad se comprueba de nuevo al guardar.",
		"resources_saved":          "Recursos actualizados.",
		"capacity_unavailable":     "No se pudo comprobar la capacidad. Revisa Docker y el worker y recarga.",
		"capacity_title":           "Capacidad del servidor",
		"capacity_hint":            "Capacidad disponible para este sitio tras descontar otros sitios y la reserva del servidor. Los sitios detenidos también cuentan. Docker Desktop muestra la capacidad de su máquina virtual.",
		"capacity_ram":             "RAM disponible",
		"capacity_cpu":             "CPU disponible",
		"capacity_total":           "Total",
		"capacity_disk":            "Espacio libre",
		"capacity_disk_hint":       "Se requieren al menos 2 GiB libres.",
		"plan_custom":              "Personalizado",
		"resource_memory":          "RAM por contenedor (MiB)",
		"resource_cpu":             "CPU por contenedor",
		"resources_per_container":  "Los límites se aplican por separado a WordPress y MariaDB. El sitio necesita el doble de la asignación seleccionada.",
		"resources_change_warning": "Los cambios detienen y recrean brevemente los dos contenedores conservando los datos. Un sitio detenido permanece detenido.",
		"resources_save":           "Guardar recursos",
		"resources_repair_first":   "Actualiza o repara el estado del sitio antes de cambiar los recursos.",
	} {
		dictionaries["es"][key] = value
	}
	for key, value := range map[string]string{
		"resources_title":          "Ressourcen",
		"resources_guide":          "CPU und RAM auswählen. Beim Speichern wird die Kapazität erneut geprüft.",
		"resources_saved":          "Ressourcen aktualisiert.",
		"capacity_unavailable":     "Kapazität nicht verfügbar. Docker und Worker prüfen und neu laden.",
		"capacity_title":           "Serverkapazität",
		"capacity_hint":            "Für diese Website verfügbare Kapazität nach Abzug anderer Websites und der Hostreserve. Gestoppte Websites zählen ebenfalls. Docker Desktop zeigt die Kapazität seiner VM.",
		"capacity_ram":             "Verfügbarer RAM",
		"capacity_cpu":             "Verfügbare CPU",
		"capacity_total":           "Gesamt",
		"capacity_disk":            "Freier Speicherplatz",
		"capacity_disk_hint":       "Mindestens 2 GiB müssen frei sein.",
		"plan_custom":              "Benutzerdefiniert",
		"resource_memory":          "RAM je Container (MiB)",
		"resource_cpu":             "CPU je Container",
		"resources_per_container":  "Die Grenzen gelten jeweils für WordPress und MariaDB. Die Website benötigt die doppelte gewählte RAM- und CPU-Zuteilung.",
		"resources_change_warning": "Die Änderungen stoppen und erstellen beide Container kurzzeitig neu; ihre Daten bleiben erhalten. Eine gestoppte Website bleibt gestoppt.",
		"resources_save":           "Ressourcen speichern",
		"resources_repair_first":   "Den Websitestatus aktualisieren oder reparieren, bevor Ressourcen geändert werden.",
	} {
		dictionaries["de"][key] = value
	}
	for key, value := range map[string]string{
		"resources_title":          "Ressources",
		"resources_guide":          "Choisissez CPU et RAM. La capacité est vérifiée à nouveau à l’enregistrement.",
		"resources_saved":          "Ressources mises à jour.",
		"capacity_unavailable":     "Capacité indisponible. Vérifiez Docker et le worker, puis rechargez.",
		"capacity_title":           "Capacité du serveur",
		"capacity_hint":            "Capacité disponible pour ce site après déduction des autres sites et de la réserve du serveur. Les sites arrêtés comptent aussi. Docker Desktop affiche la capacité de sa VM.",
		"capacity_ram":             "RAM disponible",
		"capacity_cpu":             "CPU disponible",
		"capacity_total":           "Total",
		"capacity_disk":            "Espace disque libre",
		"capacity_disk_hint":       "Au moins 2 GiB doivent rester libres.",
		"plan_custom":              "Personnalisé",
		"resource_memory":          "RAM par conteneur (MiB)",
		"resource_cpu":             "CPU par conteneur",
		"resources_per_container":  "Chaque limite s’applique séparément à WordPress et MariaDB. Le site nécessite deux fois l’allocation RAM et CPU choisie.",
		"resources_change_warning": "Les changements arrêtent et recréent brièvement les deux conteneurs avec leurs données existantes. Un site arrêté reste arrêté.",
		"resources_save":           "Enregistrer les ressources",
		"resources_repair_first":   "Actualisez ou réparez l’état du site avant de modifier les ressources.",
	} {
		dictionaries["fr"][key] = value
	}
	for key, value := range map[string]string{
		"resources_title":          "资源",
		"resources_guide":          "为此站点选择 CPU 和内存。保存时会再次检查容量。",
		"resources_saved":          "站点资源已更新。",
		"capacity_unavailable":     "无法检查容量。请检查 Docker 和工作进程，然后刷新。",
		"capacity_title":           "服务器容量",
		"capacity_hint":            "扣除其他站点和主机预留量后，此站点可用的容量。已停止的站点也计入。Docker Desktop 显示其虚拟机容量。",
		"capacity_ram":             "可分配内存",
		"capacity_cpu":             "可分配 CPU",
		"capacity_total":           "总计",
		"capacity_disk":            "可用磁盘空间",
		"capacity_disk_hint":       "至少需要 2 GiB 可用空间。",
		"plan_custom":              "自定义",
		"resource_memory":          "每个容器的内存（MiB）",
		"resource_cpu":             "每个容器的 CPU",
		"resources_per_container":  "每个限制分别应用于 WordPress 和 MariaDB。站点需要所选内存和 CPU 配额的两倍。",
		"resources_change_warning": "应用更改会短暂停止并重建两个服务容器，同时保留现有数据。已停止的站点保持停止状态。",
		"resources_save":           "保存资源",
		"resources_repair_first":   "更改资源前，请刷新或修复站点状态。",
	} {
		dictionaries["zh"][key] = value
	}
	for key, value := range map[string]string{
		"resources_title":          "リソース",
		"resources_guide":          "サイトの CPU とメモリを選択してください。保存時に容量を再確認します。",
		"resources_saved":          "リソースを更新しました。",
		"capacity_unavailable":     "容量を確認できません。Docker とワーカーを確認して再読み込みしてください。",
		"capacity_title":           "サーバー容量",
		"capacity_hint":            "他のサイトとホスト予約分を除いた、このサイトに割り当て可能な容量です。停止中のサイトも含みます。Docker Desktop は VM の容量を表示します。",
		"capacity_ram":             "割り当て可能なメモリ",
		"capacity_cpu":             "割り当て可能な CPU",
		"capacity_total":           "合計",
		"capacity_disk":            "ディスク空き容量",
		"capacity_disk_hint":       "最低 2 GiB の空き容量が必要です。",
		"plan_custom":              "カスタム",
		"resource_memory":          "コンテナごとのメモリ（MiB）",
		"resource_cpu":             "コンテナごとの CPU",
		"resources_per_container":  "各上限は WordPress と MariaDB に個別に適用されます。サイト全体で選択値の2倍のメモリと CPU 割り当てが必要です。",
		"resources_change_warning": "変更時に両方のコンテナを一時停止して既存データを保持したまま再作成します。停止中のサイトは停止したままです。",
		"resources_save":           "リソースを保存",
		"resources_repair_first":   "リソース変更前にサイトの状態を更新するか、サービスを修復してください。",
	} {
		dictionaries["ja"][key] = value
	}
}

func init() {
	for key, value := range map[string]string{"plans_title": "Resource plans", "plans_hint": "Save named plans for reuse when creating or resizing sites. Deleting a plan leaves existing sites unchanged.", "plan_name": "Plan name", "plan_add": "Add plan", "plan_delete": "Delete plan template", "plans_saved": "Resource plans updated.", "settings_page_guide": "Manage mail delivery and reusable resource plans."} {
		dictionaries["en"][key] = value
	}
	for key, value := range map[string]string{"plans_title": "پلن‌های منابع", "plans_hint": "پلن‌های نام‌دار را برای ایجاد یا تغییر منابع سایت‌ها ذخیره کنید. حذف پلن، منابع سایت‌های موجود را تغییر نمی‌دهد.", "plan_name": "نام پلن", "plan_add": "افزودن پلن", "plan_delete": "حذف قالب پلن", "plans_saved": "پلن‌های منابع به‌روز شدند.", "settings_page_guide": "ارسال ایمیل و پلن‌های قابل استفادهٔ مجدد را مدیریت کنید."} {
		dictionaries["fa"][key] = value
	}
	for key, value := range map[string]string{"plans_title": "خطط الموارد", "plans_hint": "احفظ خططًا مسماة لإنشاء المواقع أو تغيير مواردها. حذف الخطة لا يغير المواقع الحالية.", "plan_name": "اسم الخطة", "plan_add": "إضافة خطة", "plan_delete": "حذف قالب الخطة", "plans_saved": "تم تحديث خطط الموارد.", "settings_page_guide": "إدارة البريد وخطط الموارد القابلة لإعادة الاستخدام."} {
		dictionaries["ar"][key] = value
	}
	for key, value := range map[string]string{"plans_title": "Planes de recursos", "plans_hint": "Guarda planes con nombre para crear o ajustar sitios. Eliminar un plan no modifica los sitios existentes.", "plan_name": "Nombre del plan", "plan_add": "Añadir plan", "plan_delete": "Eliminar plantilla", "plans_saved": "Planes actualizados.", "settings_page_guide": "Gestiona el correo y los planes de recursos reutilizables."} {
		dictionaries["es"][key] = value
	}
	for key, value := range map[string]string{"plans_title": "Ressourcenpläne", "plans_hint": "Benannte Pläne zum Erstellen oder Anpassen von Websites speichern. Das Löschen eines Plans ändert bestehende Websites nicht.", "plan_name": "Planname", "plan_add": "Plan hinzufügen", "plan_delete": "Planvorlage löschen", "plans_saved": "Ressourcenpläne aktualisiert.", "settings_page_guide": "E-Mail-Versand und wiederverwendbare Ressourcenpläne verwalten."} {
		dictionaries["de"][key] = value
	}
	for key, value := range map[string]string{"plans_title": "Plans de ressources", "plans_hint": "Enregistrez des plans nommés pour créer ou redimensionner les sites. Supprimer un plan ne modifie pas les sites existants.", "plan_name": "Nom du plan", "plan_add": "Ajouter un plan", "plan_delete": "Supprimer le modèle", "plans_saved": "Plans mis à jour.", "settings_page_guide": "Gérez les e-mails et les plans de ressources réutilisables."} {
		dictionaries["fr"][key] = value
	}
	for key, value := range map[string]string{"plans_title": "资源方案", "plans_hint": "保存命名方案以便创建或调整站点资源。删除方案不会更改现有站点。", "plan_name": "方案名称", "plan_add": "添加方案", "plan_delete": "删除方案模板", "plans_saved": "资源方案已更新。", "settings_page_guide": "管理邮件发送和可重复使用的资源方案。"} {
		dictionaries["zh"][key] = value
	}
	for key, value := range map[string]string{"plans_title": "リソースプラン", "plans_hint": "サイト作成やリソース変更用の名前付きプランを保存します。プランを削除しても既存サイトは変わりません。", "plan_name": "プラン名", "plan_add": "プランを追加", "plan_delete": "プランテンプレートを削除", "plans_saved": "プランを更新しました。", "settings_page_guide": "メール配信と再利用可能なリソースプランを管理します。"} {
		dictionaries["ja"][key] = value
	}
}
