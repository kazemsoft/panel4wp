package i18n

func init() {
	keys := []string{"disk_quota_title", "disk_quota_not_enabled", "disk_quota_scope", "disk_quota_check_failed", "disk_quota_filesystem", "disk_quota_backend_supported", "disk_quota_development", "disk_quota_docs", "disk_quota_unsupported", "disk_quota_separate_filesystems", "disk_quota_driver"}
	translations := map[string][]string{
		"en": {
			"Hard disk quota", "Not enabled", "A site quota must cover both WordPress files and database data. Backups need separate free space.",
			"Storage support could not be verified. Check the worker and Docker, then reload.", "Storage (WordPress / database)",
			"The filesystem reports project accounting and enforcement enabled. No quota has been assigned to this site.",
			"Quota activation is in development while isolation and restart/restore safety are tested. No disk limit is currently enforced by the panel.", "Storage guide",
			"This storage does not support the proposed quota backend. It requires XFS with project quotas enabled on a supported Linux kernel.",
			"These volumes are on different filesystems; they cannot share one project quota.", "This volume driver or its mount options are outside the supported quota backend.",
		},
		"fa": {
			"سهمیهٔ سخت دیسک", "فعال نشده", "سهمیهٔ سایت باید مجموع فایل‌های وردپرس و داده‌های دیتابیس را پوشش دهد. بکاپ‌ها به فضای آزاد جداگانه نیاز دارند.",
			"پشتیبانی ذخیره‌سازی تأیید نشد. worker و Docker را بررسی و صفحه را تازه کنید.", "ذخیره‌سازی (وردپرس / دیتابیس)",
			"فایل‌سیستم، محاسبه و اعمال سهمیهٔ پروژه را فعال گزارش می‌کند. هنوز سهمیه‌ای به این سایت اختصاص داده نشده است.",
			"فعال‌سازی سهمیه در حال توسعه است و جداسازی، راه‌اندازی مجدد و بازیابی آن تست می‌شود. فعلاً پنل محدودیتی بر حجم دیسک اعمال نمی‌کند.", "راهنمای ذخیره‌سازی",
			"این ذخیره‌سازی از زیرساخت سهمیهٔ پیشنهادی پشتیبانی نمی‌کند. XFS با سهمیهٔ پروژهٔ فعال و کرنل لینوکس سازگار لازم است.",
			"این دو حجم روی فایل‌سیستم‌های متفاوت هستند و نمی‌توانند یک سهمیهٔ پروژهٔ مشترک داشته باشند.", "این درایور حجم یا گزینه‌های اتصال آن خارج از زیرساخت سهمیهٔ پشتیبانی‌شده است.",
		},
		"ar": {
			"الحصة الصارمة للقرص", "غير مفعّلة", "يجب أن تشمل حصة الموقع ملفات ووردبريس وبيانات قاعدة البيانات. تحتاج النسخ الاحتياطية إلى مساحة حرة منفصلة.",
			"تعذر التحقق من دعم التخزين. تحقق من العامل وDocker ثم أعد تحميل الصفحة.", "التخزين (ووردبريس / قاعدة البيانات)",
			"يشير نظام الملفات إلى تفعيل احتساب حصص المشاريع وتطبيقها. لم تُخصص حصة لهذا الموقع بعد.",
			"تفعيل الحصص قيد التطوير أثناء اختبار العزل وإعادة التشغيل والاستعادة. لا تطبق اللوحة حاليًا حدًا لحجم القرص.", "دليل التخزين",
			"هذا التخزين لا يدعم آلية الحصص المقترحة. يلزم XFS مع حصص مشاريع مفعّلة ونواة Linux متوافقة.",
			"وحدتا التخزين على نظامي ملفات مختلفين ولا يمكنهما مشاركة حصة مشروع واحدة.", "برنامج تشغيل وحدة التخزين أو خيارات ربطها خارج آلية الحصص المدعومة.",
		},
		"es": {
			"Cuota estricta de disco", "No activada", "La cuota debe cubrir los archivos de WordPress y los datos de la base de datos. Las copias de seguridad necesitan espacio libre aparte.",
			"No se pudo verificar el soporte del almacenamiento. Comprueba el worker y Docker y recarga la página.", "Almacenamiento (WordPress / base de datos)",
			"El sistema de archivos indica que el cómputo y la aplicación de cuotas de proyecto están activados. Este sitio aún no tiene cuota asignada.",
			"La activación de cuotas está en desarrollo mientras se prueban el aislamiento, los reinicios y la restauración. El panel no aplica actualmente un límite de disco.", "Guía de almacenamiento",
			"Este almacenamiento no admite el mecanismo de cuotas propuesto. Requiere XFS con cuotas de proyecto activadas y un kernel Linux compatible.",
			"Los volúmenes están en sistemas de archivos distintos y no pueden compartir una cuota de proyecto.", "El controlador del volumen o sus opciones de montaje están fuera del mecanismo de cuotas admitido.",
		},
		"de": {
			"Festes Speicherlimit", "Nicht aktiviert", "Ein Website-Limit muss WordPress-Dateien und Datenbankdaten zusammen erfassen. Backups benötigen zusätzlichen freien Platz.",
			"Die Speicherunterstützung konnte nicht bestätigt werden. Worker und Docker prüfen und die Seite neu laden.", "Speicher (WordPress / Datenbank)",
			"Das Dateisystem meldet aktive Erfassung und Durchsetzung von Projektquoten. Dieser Website wurde noch keine Quote zugewiesen.",
			"Die Aktivierung wird entwickelt; Isolation, Neustart und Wiederherstellung werden getestet. Das Panel setzt derzeit kein Speicherlimit durch.", "Speicheranleitung",
			"Dieser Speicher unterstützt das geplante Quota-Verfahren nicht. Erforderlich sind XFS mit aktiven Projektquoten und ein geeigneter Linux-Kernel.",
			"Die Volumes liegen auf unterschiedlichen Dateisystemen und können keine gemeinsame Projektquote verwenden.", "Dieser Volume-Treiber oder seine Mount-Optionen werden vom Quota-Verfahren nicht unterstützt.",
		},
		"fr": {
			"Quota strict de disque", "Non activé", "Le quota doit couvrir les fichiers WordPress et les données de la base. Les sauvegardes nécessitent un espace libre distinct.",
			"Le support du stockage n’a pas pu être vérifié. Vérifiez le worker et Docker, puis rechargez la page.", "Stockage (WordPress / base de données)",
			"Le système de fichiers indique que le comptage et l’application des quotas de projet sont actifs. Aucun quota n’a encore été attribué à ce site.",
			"L’activation des quotas est en développement pendant les tests d’isolation, de redémarrage et de restauration. Le panneau n’applique actuellement aucune limite de disque.", "Guide du stockage",
			"Ce stockage ne prend pas en charge le mécanisme de quotas prévu. Il nécessite XFS avec quotas de projet actifs et un noyau Linux compatible.",
			"Ces volumes sont sur des systèmes de fichiers différents et ne peuvent pas partager un quota de projet.", "Ce pilote de volume ou ses options de montage ne sont pas pris en charge par le mécanisme de quotas.",
		},
		"zh": {
			"磁盘硬配额", "未启用", "站点配额必须同时涵盖 WordPress 文件和数据库数据。备份需要独立的可用空间。",
			"无法验证存储支持。请检查工作进程和 Docker，然后刷新页面。", "存储（WordPress / 数据库）",
			"文件系统报告已启用项目配额计量和强制执行。此站点尚未分配配额。",
			"配额启用功能正在开发中，隔离、重启和恢复安全性仍在测试。面板目前不强制执行磁盘容量限制。", "存储指南",
			"此存储不支持计划中的配额机制。需要启用项目配额的 XFS 和兼容的 Linux 内核。",
			"这两个卷位于不同的文件系统，无法共用一个项目配额。", "此卷驱动或其挂载选项不在配额机制支持范围内。",
		},
		"ja": {
			"ディスクのハードクォータ", "未有効化", "サイトのクォータは WordPress ファイルとデータベースを合わせて制限する必要があります。バックアップには別途空き容量が必要です。",
			"ストレージの対応状況を確認できませんでした。ワーカーと Docker を確認して再読み込みしてください。", "ストレージ（WordPress / データベース）",
			"ファイルシステムはプロジェクトクォータの集計と強制適用が有効と報告しています。このサイトにはまだクォータが割り当てられていません。",
			"クォータ有効化は開発中です。分離、再起動、復元の安全性をテストしています。現在、パネルはディスク容量を制限していません。", "ストレージガイド",
			"このストレージは予定しているクォータ方式に対応していません。プロジェクトクォータが有効な XFS と対応する Linux カーネルが必要です。",
			"この二つのボリュームは異なるファイルシステムにあり、同じプロジェクトクォータを共有できません。", "このボリュームドライバーまたはマウントオプションはクォータ方式の対応範囲外です。",
		},
	}
	for language, values := range translations {
		if len(values) != len(keys) {
			panic("incomplete storage translations: " + language)
		}
		for index, key := range keys {
			dictionaries[language][key] = values[index]
		}
	}
}
