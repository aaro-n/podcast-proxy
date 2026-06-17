package main

// GetXSLTemplate 返回极其精美、支持播放、搜索、自适应的 XSL 模板
func GetXSLTemplate() string {
	return `<?xml version="1.0" encoding="utf-8"?>
<xsl:stylesheet version="1.0" 
    xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
    xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"
    xmlns:content="http://purl.org/rss/1.0/modules/content/"
    xmlns:atom="http://www.w3.org/2005/Atom"
    exclude-result-prefixes="itunes content atom">
    
    <xsl:output method="html" encoding="utf-8" indent="yes" />
    
    <xsl:template match="/">
        <html lang="zh-CN">
            <head>
                <meta charset="utf-8" />
                <meta name="viewport" content="width=device-width, initial-scale=1.0" />
                <title><xsl:value-of select="rss/channel/title"/> - Podcast Proxy</title>
                <link href="https://lib.baomitu.com/tailwindcss/2.2.19/tailwind.min.css" rel="stylesheet" />
                <link rel="stylesheet" href="https://lib.baomitu.com/font-awesome/6.4.0/css/all.min.css" />
                <style>
                    body {
                        background-color: #0f172a;
                        color: #f1f5f9;
                        font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
                    }
                    .glass {
                        background: rgba(30, 41, 59, 0.7);
                        backdrop-filter: blur(12px);
                        -webkit-backdrop-filter: blur(12px);
                        border: 1px solid rgba(255, 255, 255, 0.06);
                    }
                    .glass-hover {
                        transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
                    }
                    .glass-hover:hover {
                        background: rgba(30, 41, 59, 0.95);
                        border-color: rgba(96, 165, 250, 0.3);
                        transform: translateY(-2px);
                    }
                    .custom-scrollbar::-webkit-scrollbar {
                        width: 6px;
                        height: 6px;
                    }
                    .custom-scrollbar::-webkit-scrollbar-track {
                        background: transparent;
                    }
                    .custom-scrollbar::-webkit-scrollbar-thumb {
                        background: #475569;
                        border-radius: 4px;
                    }
                    .custom-scrollbar::-webkit-scrollbar-thumb:hover {
                        background: #64748b;
                    }
                    .rich-content img {
                        max-width: 100%;
                        height: auto;
                        border-radius: 0.5rem;
                        margin: 1rem 0;
                    }
                    .rich-content a {
                        color: #60a5fa;
                        text-decoration: underline;
                    }
                    .rich-content a:hover {
                        color: #93c5fd;
                    }
                    .rich-content p {
                        margin-bottom: 0.75rem;
                    }
                    .rich-content ul, .rich-content ol {
                        margin-left: 1.5rem;
                        margin-bottom: 0.75rem;
                        list-style-type: disc;
                    }
                    .rich-content ol {
                        list-style-type: decimal;
                    }
                </style>
            </head>
            <body class="min-h-screen flex flex-col custom-scrollbar pb-24">
                <!-- 模糊背景 -->
                <div class="fixed inset-0 z-0 overflow-hidden pointer-events-none opacity-20">
                    <div class="absolute inset-0 bg-cover bg-center filter blur-3xl scale-110" id="bg-layer">
                        <xsl:choose>
                            <xsl:when test="rss/channel/itunes:image/@href">
                                <xsl:attribute name="style">background-image: url('<xsl:value-of select="rss/channel/itunes:image/@href"/>');</xsl:attribute>
                            </xsl:when>
                            <xsl:when test="rss/channel/image/url">
                                <xsl:attribute name="style">background-image: url('<xsl:value-of select="rss/channel/image/url"/>');</xsl:attribute>
                            </xsl:when>
                        </xsl:choose>
                    </div>
                    <div class="absolute inset-0 bg-slate-950/70"></div>
                </div>

                <!-- 内容区 -->
                <div class="relative z-10 flex-grow container mx-auto px-4 py-8 lg:py-12 max-w-7xl">
                    <div class="grid grid-cols-1 lg:grid-cols-12 gap-8">
                        
                        <!-- 左边栏：播客封面与基础信息 -->
                        <div class="lg:col-span-4 lg:sticky lg:top-8 self-start">
                            <div class="glass rounded-3xl p-6 shadow-2xl flex flex-col items-center text-center">
                                <!-- 封面 -->
                                <div class="w-48 h-48 md:w-64 md:h-64 rounded-2xl overflow-hidden shadow-2xl mb-6 relative group border border-slate-700">
                                    <img class="w-full h-full object-cover" alt="Podcast Cover">
                                        <xsl:attribute name="src">
                                            <xsl:choose>
                                                <xsl:when test="rss/channel/itunes:image/@href"><xsl:value-of select="rss/channel/itunes:image/@href"/></xsl:when>
                                                <xsl:when test="rss/channel/image/url"><xsl:value-of select="rss/channel/image/url"/></xsl:when>
                                                <xsl:otherwise>https://cdn.bootcdn.net/ajax/libs/font-awesome/6.4.0/svgs/solid/microphone.svg</xsl:otherwise>
                                            </xsl:choose>
                                        </xsl:attribute>
                                    </img>
                                </div>
                                
                                <!-- 标题与作者 -->
                                <h1 class="text-2xl font-extrabold text-white tracking-tight mb-2">
                                    <xsl:value-of select="rss/channel/title"/>
                                </h1>
                                <p class="text-blue-400 font-medium text-sm mb-4">
                                    <xsl:choose>
                                        <xsl:when test="rss/channel/itunes:author"><xsl:value-of select="rss/channel/itunes:author"/></xsl:when>
                                        <xsl:when test="rss/channel/itunes:owner/itunes:name"><xsl:value-of select="rss/channel/itunes:owner/itunes:name"/></xsl:when>
                                        <xsl:otherwise>未知作者</xsl:otherwise>
                                    </xsl:choose>
                                </p>

                                <!-- 分类与语言 -->
                                <div class="flex flex-wrap justify-center gap-2 mb-6">
                                    <xsl:if test="rss/channel/itunes:category/@text">
                                        <span class="px-3 py-1 bg-slate-800 text-slate-300 text-xs font-semibold rounded-full border border-slate-700">
                                            <xsl:value-of select="rss/channel/itunes:category/@text"/>
                                        </span>
                                    </xsl:if>
                                    <xsl:if test="rss/channel/language">
                                        <span class="px-3 py-1 bg-slate-800 text-slate-300 text-xs font-semibold rounded-full border border-slate-700 uppercase">
                                            <xsl:value-of select="rss/channel/language"/>
                                        </span>
                                    </xsl:if>
                                    <span class="px-3 py-1 bg-blue-900/40 text-blue-300 text-xs font-semibold rounded-full border border-blue-800/50">
                                        代理订阅源
                                    </span>
                                </div>

                                <!-- 播客简介 -->
                                <div class="text-slate-300 text-sm leading-relaxed text-left w-full border-t border-slate-800 pt-4 mb-6 max-h-48 overflow-y-auto custom-scrollbar">
                                    <xsl:choose>
                                        <xsl:when test="rss/channel/itunes:summary">
                                            <xsl:value-of select="rss/channel/itunes:summary"/>
                                        </xsl:when>
                                        <xsl:otherwise>
                                            <xsl:value-of select="rss/channel/description" disable-output-escaping="yes"/>
                                        </xsl:otherwise>
                                    </xsl:choose>
                                </div>

                                <!-- 复制RSS链接 -->
                                <button onclick="copyRSSUrl()" class="w-full py-3 px-4 bg-gradient-to-r from-blue-600 to-indigo-600 hover:from-blue-500 hover:to-indigo-500 text-white font-semibold rounded-xl shadow-lg transition duration-200 transform hover:scale-[1.02] flex items-center justify-center gap-2">
                                    <i class="fa-solid fa-square-rss"></i> 复制代理 RSS 链接
                                </button>
                                <p class="text-slate-400 text-xs mt-3">
                                    将此 RSS 链接复制到播客客户端（如 Pocket Casts、小宇宙等）即可订阅。
                                </p>
                            </div>
                        </div>

                        <!-- 右侧单集列表 -->
                        <div class="lg:col-span-8 flex flex-col gap-6">
                            
                            <!-- 搜索与计数 -->
                            <div class="glass rounded-2xl p-4 flex flex-col sm:flex-row items-center justify-between gap-4 shadow-lg">
                                <div class="text-sm text-slate-300 flex items-center gap-2 font-medium">
                                    <i class="fa-solid fa-microphone-lines text-blue-400 text-lg"></i>
                                    共计 <span id="episode-count" class="text-white font-extrabold text-base"><xsl:value-of select="count(rss/channel/item)"/></span> 期节目
                                </div>
                                <div class="relative w-full sm:w-64">
                                    <span class="absolute inset-y-0 left-0 flex items-center pl-3 pointer-events-none text-slate-400">
                                        <i class="fa-solid fa-magnifying-glass"></i>
                                    </span>
                                    <input type="text" id="search-input" oninput="filterEpisodes()" placeholder="搜索单集标题或内容..." class="w-full pl-9 pr-4 py-2 bg-slate-800 border border-slate-700 rounded-xl text-slate-200 text-sm focus:outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500 transition duration-150" />
                                </div>
                            </div>

                            <!-- 单集卡片 -->
                            <div class="flex flex-col gap-4" id="episode-list">
                                <xsl:for-each select="rss/channel/item">
                                    <div class="glass glass-hover rounded-2xl p-6 shadow-md flex flex-col gap-4 episode-card" data-title="{title}" data-desc="{description}">
                                        
                                        <!-- 发布时间、时长 -->
                                        <div class="flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-slate-400 font-medium">
                                            <span class="flex items-center gap-1 bg-slate-800/50 px-2 py-1 rounded border border-slate-700/50 raw-date">
                                                <i class="fa-regular fa-calendar-days"></i> <xsl:value-of select="pubDate"/>
                                            </span>
                                            <xsl:if test="itunes:duration">
                                                <span class="flex items-center gap-1 bg-slate-800/50 px-2 py-1 rounded border border-slate-700/50">
                                                    <i class="fa-regular fa-clock"></i> <xsl:value-of select="itunes:duration"/>
                                                </span>
                                            </xsl:if>
                                        </div>

                                        <!-- 标题 -->
                                        <h2 class="text-xl font-bold text-white leading-snug hover:text-blue-400 transition cursor-pointer">
                                            <xsl:value-of select="title"/>
                                        </h2>

                                        <!-- 富文本描述 -->
                                        <div class="rich-content text-slate-300 text-sm leading-relaxed max-h-24 overflow-hidden relative transition-all duration-300 desc-container">
                                            <xsl:choose>
                                                <xsl:when test="content:encoded">
                                                    <xsl:value-of select="content:encoded" disable-output-escaping="yes"/>
                                                </xsl:when>
                                                <xsl:otherwise>
                                                    <xsl:value-of select="description" disable-output-escaping="yes"/>
                                                </xsl:otherwise>
                                            </xsl:choose>
                                            
                                            <!-- 折叠遮罩 -->
                                            <div class="absolute bottom-0 inset-x-0 h-10 bg-gradient-to-t from-slate-900/90 to-transparent pointer-events-none desc-fade"></div>
                                        </div>

                                        <!-- 控制栏 -->
                                        <div class="flex flex-wrap items-center justify-between gap-4 mt-2 pt-2 border-t border-slate-800">
                                            <button onclick="toggleExpand(this)" class="text-xs text-blue-400 hover:text-blue-300 font-semibold flex items-center gap-1 focus:outline-none">
                                                <span>展开查看全文</span> <i class="fa-solid fa-chevron-down transition"></i>
                                            </button>

                                            <!-- 播放按钮 -->
                                            <xsl:if test="enclosure/@url">
                                                <button class="py-2 px-5 bg-blue-600 hover:bg-blue-500 text-white font-semibold text-sm rounded-lg flex items-center gap-2 shadow-md transition transform hover:scale-[1.03] active:scale-95"
                                                    onclick="playEpisode(this)">
                                                    <xsl:attribute name="data-audio-url"><xsl:value-of select="enclosure/@url"/></xsl:attribute>
                                                    <xsl:attribute name="data-title"><xsl:value-of select="title"/></xsl:attribute>
                                                    <xsl:attribute name="data-cover">
                                                        <xsl:choose>
                                                            <xsl:when test="itunes:image/@href"><xsl:value-of select="itunes:image/@href"/></xsl:when>
                                                            <xsl:when test="../itunes:image/@href"><xsl:value-of select="../itunes:image/@href"/></xsl:when>
                                                            <xsl:when test="../image/url"><xsl:value-of select="../image/url"/></xsl:when>
                                                            <xsl:otherwise></xsl:otherwise>
                                                        </xsl:choose>
                                                    </xsl:attribute>
                                                    <i class="fa-solid fa-play"></i> 立即播放
                                                </button>
                                            </xsl:if>
                                        </div>
                                    </div>
                                </xsl:for-each>
                            </div>
                        </div>
                    </div>
                </div>

                <!-- 全局底部悬浮播放器 -->
                <div id="global-player" class="fixed bottom-0 inset-x-0 glass border-t border-slate-800 p-4 shadow-2xl z-50 transform translate-y-full transition-transform duration-300 flex flex-col md:flex-row items-center gap-4 justify-between">
                    <div class="flex items-center gap-3 w-full md:w-1/3">
                        <img id="player-cover" class="w-12 h-12 rounded-lg object-cover border border-slate-700 shadow-md flex-shrink-0" src="" alt="Cover" />
                        <div class="overflow-hidden">
                            <div id="player-title" class="text-white font-bold text-sm truncate">单集标题</div>
                            <div id="player-author" class="text-slate-400 text-xs mt-0.5 truncate">
                                <xsl:choose>
                                    <xsl:when test="rss/channel/title"><xsl:value-of select="rss/channel/title"/></xsl:when>
                                    <xsl:otherwise>播客节目</xsl:otherwise>
                                </xsl:choose>
                            </div>
                        </div>
                    </div>

                    <div class="w-full md:w-2/3 flex items-center justify-end">
                        <audio id="main-audio" controls="controls" class="w-full max-w-2xl h-10 outline-none" style="filter: invert(0.9) hue-rotate(180deg);"></audio>
                    </div>
                </div>

                <!-- JavaScript -->
                <script>
                    document.addEventListener("DOMContentLoaded", () => {
                        const dateNodes = document.querySelectorAll(".raw-date");
                        dateNodes.forEach(node => {
                            try {
                                const raw = node.innerText.trim();
                                if (raw) {
                                    const parsed = new Date(raw);
                                    if (!isNaN(parsed.getTime())) {
                                        const formatted = parsed.toLocaleDateString("zh-CN", {
                                            year: "numeric",
                                            month: "long",
                                            day: "numeric"
                                        });
                                        node.innerHTML = ` + "`" + `<i class="fa-regular fa-calendar-days"></i> ` + "`" + ` + formatted;
                                    }
                                }
                            } catch(e) {}
                        });
                    });

                    function copyRSSUrl() {
                        const currentUrl = window.location.href;
                        navigator.clipboard.writeText(currentUrl).then(() => {
                            alert("已复制代理 RSS 链接到剪贴板！\n" + currentUrl);
                        }).catch(err => {
                            const input = document.createElement("input");
                            input.value = currentUrl;
                            document.body.appendChild(input);
                            input.select();
                            document.execCommand("copy");
                            document.body.removeChild(input);
                            alert("已复制代理 RSS 链接到剪贴板！");
                        });
                    }

                    const globalPlayer = document.getElementById("global-player");
                    const mainAudio = document.getElementById("main-audio");
                    const playerCover = document.getElementById("player-cover");
                    const playerTitle = document.getElementById("player-title");
                    let activePlayBtn = null;

                    function playEpisode(btn) {
                        const audioUrl = btn.getAttribute("data-audio-url");
                        const title = btn.getAttribute("data-title");
                        const cover = btn.getAttribute("data-cover");

                        if (activePlayBtn) {
                            activePlayBtn.innerHTML = ` + "`" + `<i class="fa-solid fa-play"></i> 立即播放` + "`" + `;
                            activePlayBtn.classList.remove("bg-green-600", "hover:bg-green-500");
                            activePlayBtn.classList.add("bg-blue-600", "hover:bg-blue-500");
                        }

                        if (mainAudio.src === audioUrl) {
                            if (!mainAudio.paused) {
                                mainAudio.pause();
                                btn.innerHTML = ` + "`" + `<i class="fa-solid fa-play"></i> 恢复播放` + "`" + `;
                            } else {
                                mainAudio.play();
                                btn.innerHTML = ` + "`" + `<i class="fa-solid fa-pause"></i> 正在播放` + "`" + `;
                                btn.classList.remove("bg-blue-600", "hover:bg-blue-500");
                                btn.classList.add("bg-green-600", "hover:bg-green-500");
                                activePlayBtn = btn;
                            }
                            return;
                        }

                        mainAudio.src = audioUrl;
                        playerTitle.innerText = title;
                        playerCover.src = cover || "https://lib.baomitu.com/font-awesome/6.4.0/svgs/solid/microphone.svg";

                        globalPlayer.classList.remove("translate-y-full");

                        mainAudio.play();
                        btn.innerHTML = ` + "`" + `<i class="fa-solid fa-pause"></i> 正在播放` + "`" + `;
                        btn.classList.remove("bg-blue-600", "hover:bg-blue-500");
                        btn.classList.add("bg-green-600", "hover:bg-green-500");
                        activePlayBtn = btn;
                    }

                    mainAudio.addEventListener("pause", () => {
                        if (activePlayBtn) {
                            activePlayBtn.innerHTML = ` + "`" + `<i class="fa-solid fa-play"></i> 恢复播放` + "`" + `;
                        }
                    });
                    mainAudio.addEventListener("play", () => {
                        if (activePlayBtn) {
                            activePlayBtn.innerHTML = ` + "`" + `<i class="fa-solid fa-pause"></i> 正在播放` + "`" + `;
                        }
                    });

                    function toggleExpand(btn) {
                        const card = btn.closest(".episode-card");
                        const desc = card.querySelector(".desc-container");
                        const fade = card.querySelector(".desc-fade");
                        const text = btn.querySelector("span");
                        const icon = btn.querySelector("i");

                        if (desc.style.maxHeight === "none") {
                            desc.style.maxHeight = "6rem";
                            fade.classList.remove("hidden");
                            text.innerText = "展开查看全文";
                            icon.classList.remove("rotate-180");
                        } else {
                            desc.style.maxHeight = "none";
                            fade.classList.add("hidden");
                            text.innerText = "折叠全文";
                            icon.classList.add("rotate-180");
                        }
                    }

                    function filterEpisodes() {
                        const query = document.getElementById("search-input").value.toLowerCase();
                        const cards = document.querySelectorAll(".episode-card");
                        let matchCount = 0;

                        cards.forEach(card => {
                            const title = card.getAttribute("data-title").toLowerCase();
                            const desc = card.getAttribute("data-desc").toLowerCase();

                            if (title.includes(query) || desc.includes(query)) {
                                card.classList.remove("hidden");
                                matchCount++;
                            } else {
                                card.classList.add("hidden");
                            }
                        });

                        document.getElementById("episode-count").innerText = matchCount;
                    }
                </script>
            </body>
        </html>
    </xsl:template>
</xsl:stylesheet>`
}
