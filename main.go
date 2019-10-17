package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"time"

	"html/template"

	"github.com/essentialbooks/books/pkg/common"
	"github.com/kjk/notionapi"
	"github.com/kjk/notionapi/caching_downloader"
	"github.com/kjk/u"
	"github.com/tdewolff/minify"
	"github.com/tdewolff/minify/css"
	"github.com/tdewolff/minify/html"
	"github.com/tdewolff/minify/js"
)

var (
	soUserIDToNameMap map[int]string
	googleAnalytics   template.HTML
	doMinify          bool
	minifier          *minify.M

	notionAuthToken string

	// when downloading pages from the server, count total number of
	// downloaded and those from cache
	nTotalDownloaded int
	nTotalFromCache  int
)

var (
	booksMain = []*Book{
		bookGo,
		bookCpp,
		bookJavaScript,
		bookCSS,
		bookHTML,
		bookHTMLCanvas,
		bookJava,
		bookKotlin,
		bookCsharp,
		bookPython,
		bookPostgresql,
		bookMysql,
		bookIOS,
		bookAndroid,
		bookBash,
		bookPowershell,
		bookBatch,
		bookGit,
		bookPHP,
		bookRuby,
		bookNode,
		bookDart,
		bookTypeScript,
		bookSwift,
	}
	booksUnpublished = []*Book{
		bookNETFramework,
		bookAlgorithm,
		bookC,
		bookObjectiveC,
		bookReact,
		bookReactNative,
		bookRubyOnRails,
		bookSql,
	}
	allBooks = append(booksMain, booksUnpublished...)
)

var (
	nProcessed            = 0
	nNotionPagesFromCache = 0
	nDownloadedPages      = 0
)

func eventObserver(ev interface{}) {
	switch v := ev.(type) {
	case *caching_downloader.EventError:
		logf(v.Error)
	case *caching_downloader.EventDidDownload:
		nProcessed++
		nDownloadedPages++
		logf("%03d '%s' : downloaded in %s\n", nProcessed, v.PageID, v.Duration)
	case *caching_downloader.EventDidReadFromCache:
		nProcessed++
		nNotionPagesFromCache++
		// TODO: only verbose
		//logf("%03d '%s' : read from cache in %s\n", nProcessed, v.PageID, v.Duration)
	case *caching_downloader.EventGotVersions:
		logf("downloaded info about %d versions in %s\n", v.Count, v.Duration)
	}
}

func (book *Book) afterPageDownload(page *notionapi.Page) error {
	id := toNoDashID(page.ID)
	p := &Page{
		NotionPage: page,
		NotionID:   id,
	}
	book.idToPage[id] = p
	downloadImages(book, p)
	return nil
}

func downloadBook(book *Book) {
	logf("Loading %s...\n", book.Title)
	nProcessed = 0
	nNotionPagesFromCache = 0
	nDownloadedPages = 0

	book.client = newNotionClient()
	cacheDir := book.NotionCacheDir()
	dirCache, err := caching_downloader.NewDirectoryCache(cacheDir)
	must(err)
	d := caching_downloader.New(dirCache, book.client)
	d.EventObserver = eventObserver
	d.RedownloadNewerVersions = flgDownload
	d.NoReadCache = flgDisableNotionCache

	startPageID := book.NotionStartPageID
	pages, err := d.DownloadPagesRecursively(startPageID, book.afterPageDownload)
	must(err)
	nPages := len(pages)
	logf("Got %d pages for %s, downloaded: %d, from cache: %d\n", nPages, book.Title, nDownloadedPages, nNotionPagesFromCache)
	bookFromPages(book)
}

func loadSOUserMappingsMust() {
	path := filepath.Join("stack-overflow-docs-dump", "users.json.gz")
	err := common.JSONDecodeGzipped(path, &soUserIDToNameMap)
	u.PanicIfErr(err)
}

func shouldCopyImage(path string) bool {
	return !strings.Contains(path, "@2x")
}

func copyCoversMust() {
	srcDir := "covers"
	dstDir := filepath.Join("www", "covers")
	u.CopyDirRecurMust(dstDir, srcDir, shouldCopyImage)
	dstDir = filepath.Join("www", "covers_small")
	srcDir = filepath.Join("covers", "covers_small")
	u.CopyDirRecurMust(dstDir, srcDir, shouldCopyImage)
}

func genBooks(books []*Book) {
	timeStart := time.Now()
	clearSitemapURLS()
	copyCoversMust()

	_ = genIndex(books, nil)
	_ = genIndexGrid(books, nil)
	gen404TopLevel()
	_ = genAbout(nil)
	_ = genFeedback(nil)

	if false {
		// parallel
		n := runtime.NumCPU()
		sem := make(chan bool, n)
		var wd sync.WaitGroup
		for _, book := range books {
			wd.Add(1)
			go func(b *Book) {
				sem <- true
				genBook(b)
				<-sem
				wd.Done()
			}(book)
		}
		wd.Wait()
	} else {
		for _, book := range books {
			genBook(book)
		}
	}
	writeSitemap()
	logf("Finished generating all books in %s\n", time.Since(timeStart))
}

func initMinify() {
	minifier = minify.New()
	minifier.AddFunc("text/css", css.Minify)
	minifier.AddFunc("text/html", html.Minify)
	minifier.AddFunc("text/javascript", js.Minify)
	// less aggresive minification because html validators
	// report this as html errors
	minifier.Add("text/html", &html.Minifier{
		KeepDocumentTags: true,
		KeepEndTags:      true,
	})

	doMinify = true
	if flgPreviewOnDemand || flgPreviewStatic {
		doMinify = false
	}
}

func initBook(book *Book) {
	var err error

	u.CreateDirMust(book.OutputCacheDir())
	u.CreateDirMust(book.NotionCacheDir())

	if false {
		loadCache("cache/go/cache.txt")
		os.Exit(0)
	}

	book.idToPage = map[string]*Page{}
	book.cache = loadCache(book.cachePath())
	must(err)
}

func findBook(id string) *Book {
	for _, book := range allBooks {
		// fuzzy match - whatever hits
		parts := []string{book.Title, book.Dir, book.NotionStartPageID}
		for _, s := range parts {
			if strings.EqualFold(s, id) {
				return book
			}
		}
	}
	return nil
}

func adHoc() {
	if false {
		glotRunTestAndExit()
	}
	if false {
		glotGetSnippedIDTestAndExit()
	}

	// only needs to be run when we add new covers
	if false {
		genTwitterImagesAndExit()
	}
	if false {
		genSmallCoversAndExit()
	}
}

func isPreview() bool {
	return flgPreviewStatic || flgPreviewOnDemand
}

var (
	// url or id of the page to rebuild
	flgNoUpdateOutput bool
	// if true, disables notion cache, forcing re-download of notion page
	// even if cached verison on disk exits
	flgDisableNotionCache       bool
	flgDownload                 bool
	flgPreviewStatic            bool
	flgPreviewOnDemand          bool
	flgReportStackOverflowLinks bool
)

func main() {
	var (
		flgAnalytics string
		flgWc        bool
		flgGen       bool
		flgAllBooks  bool
		// if true, disables downloading pages
		flgNoDownload bool

		flgReportExternalLinks bool
		flgProfile             bool
		flgDeployDraft         bool
		flgDeployProd          bool
	)

	{
		flag.StringVar(&flgAnalytics, "analytics", "", "google analytics code")
		flag.BoolVar(&flgPreviewStatic, "preview-static", false, "if true starts web server for previewing locally generated static html")
		flag.BoolVar(&flgPreviewOnDemand, "preview-on-demand", false, "if true will start web server for previewing the book locally")
		flag.BoolVar(&flgAllBooks, "all-books", false, "if true will do all books")
		flag.BoolVar(&flgNoUpdateOutput, "no-update-output", false, "if true, will disable updating ouput files in cache")
		flag.BoolVar(&flgDisableNotionCache, "no-cache", false, "if true, disables cache for notion")
		flag.BoolVar(&flgNoDownload, "no-download", false, "if true, will not download pages from notion")
		flag.BoolVar(&flgReportExternalLinks, "report-external-links", false, "if true, shows external links for all pages")
		flag.BoolVar(&flgReportStackOverflowLinks, "report-so-links", false, "if true, shows links to stackoverflow.com")
		flag.BoolVar(&flgWc, "wc", false, "wc -l")
		flag.BoolVar(&flgDownload, "dl", false, "download a given book, 'all' for all books")
		flag.BoolVar(&flgGen, "gen", false, "generate html for the book")
		flag.BoolVar(&flgProfile, "prof", false, "write cpu profile")
		flag.BoolVar(&flgDeployDraft, "deploy-draft", false, "deploy to netlify as draft")
		flag.BoolVar(&flgDeployProd, "deploy-prod", false, "deploy to netlify production")
		flag.Parse()

		if flgAnalytics != "" {
			googleAnalyticsTmpl := `<script async src="https://www.googletagmanager.com/gtag/js?id=%s"></script>
			<script>
				window.dataLayer = window.dataLayer || [];
				function gtag(){dataLayer.push(arguments);}
				gtag('js', new Date());
				gtag('config', '%s')
			</script>
		`
			s := fmt.Sprintf(googleAnalyticsTmpl, flgAnalytics, flgAnalytics)
			googleAnalytics = template.HTML(s)
		}
	}

	if false {
		testHang()
		return
	}
	adHoc()

	if flgWc {
		doLineCount()
		return
	}

	closeLog := openLog()
	defer closeLog()

	{
		notionAuthToken = os.Getenv("NOTION_TOKEN")
		if notionAuthToken != "" {
			logf("NOTION_TOKEN provided, can write back\n")
		} else {
			logf("NOTION_TOKEN not provided, read only\n")
		}
	}

	notionapi.LogFunc = logf

	_ = os.RemoveAll("www")
	u.CreateDirMust(filepath.Join("www", "s"))
	u.CreateDirMust("log")

	timeStart := time.Now()

	initMinify()
	loadSOUserMappingsMust()

	if flgReportExternalLinks || flgReportStackOverflowLinks {
		reportExternalLinks()
		return
	}

	if flgDeployDraft || flgDeployProd {
		flgAllBooks = true
		flgGen = true
	}

	valid := flgDownload || flgPreviewOnDemand || flgPreviewStatic || flgGen
	if !valid {
		flag.Usage()
		return
	}

	if flgGen {
		os.RemoveAll("www")
		os.MkdirAll(filepath.Join("www", "s"), 0755)
		os.MkdirAll(filepath.Join("www", "gen"), 0755)
	}

	buildFrontend()

	if flgProfile {
		profileName := "bookgen.prof"
		f, err := os.Create(profileName)
		must(err)
		err = pprof.StartCPUProfile(f)
		must(err)
		defer func() {
			u.CloseNoError(f)
			logf("CPU profile saved to a file '%s'\n", profileName)
		}()
		defer func() {
			pprof.StopCPUProfile()
			logf("stopped cpu profile\n")
		}()
	}

	books := booksMain
	if flgAllBooks {
		books = allBooks
		logf("Downloading all books\n")
	} else {
		if len(flag.Args()) > 0 {
			var newBooks []*Book
			for _, name := range flag.Args() {
				book := findBook(name)
				if book == nil {
					logf("Didn't find book named '%s'\n", name)
					continue
				}
				newBooks = append(newBooks, book)
			}
			if len(newBooks) > 0 {
				books = newBooks
				logf("Downloading %d books", len(books))
				for _, b := range books {
					logf(" %s", b.Title)
				}
				logf("\n")
			}
		}
	}

	for _, book := range books {
		initBook(book)
		downloadBook(book)
		loadSoContributorsMust(book)
		calcBookPageHeadings(book)
	}
	logf("Downloaded %d pages, %d from cache, in %s\n", nTotalDownloaded, nTotalFromCache, time.Since(timeStart))

	if flgGen || flgPreviewStatic {
		genStartTime := time.Now()
		genBooks(books)
		genNetlifyHeaders()
		genNetlifyRedirects(books)
		printAndClearErrors()
		logf("Gen time: %s, total time: %s\n", time.Since(genStartTime), time.Since(timeStart))
	}

	if flgDeployDraft {
		cmd := exec.Command("netlify", "deploy", "--dir=www", "--site=7df32685-1421-41cf-937a-a92fde6725f4", "--open")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		u.RunCmdMust(cmd)
		return
	}

	if flgDeployProd {
		cmd := exec.Command("netlify", "deploy", "--prod", "--dir=www", "--site=7df32685-1421-41cf-937a-a92fde6725f4", "--open")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		u.RunCmdMust(cmd)
		return
	}

	if flgPreviewOnDemand {
		logf("Time: %s\n", time.Since(timeStart))
		startPreviewOnDemand(books)
		return
	}

	if flgPreviewStatic {
		startPreviewStatic()
	}
}

func newNotionClient() *notionapi.Client {
	client := &notionapi.Client{
		AuthToken: notionAuthToken,
	}
	// client.Logger = logFile
	return client
}
