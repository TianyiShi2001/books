package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"html/template"

	"github.com/essentialbooks/books/pkg/common"
	"github.com/kjk/notionapi"
	"github.com/kjk/u"
	"github.com/tdewolff/minify"
	"github.com/tdewolff/minify/css"
	"github.com/tdewolff/minify/html"
	"github.com/tdewolff/minify/js"
)

var (
	flgAnalytics string
	flgPreview   bool
	flgAllBooks  bool
	flgNoCache   bool
	// url or id of the page to rebuild
	flgRebuildOnePage      string
	flgUpdateOutput        bool
	flgRedownloadReplit    bool
	flgRedownloadOne       string
	flgRedownloadBook      string
	flgRedownloadOneReplit string
	flgVerbose             bool

	soUserIDToNameMap map[int]string
	googleAnalytics   template.HTML
	doMinify          bool
	minifier          *minify.M

	notionAuthToken string
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
		bookPowershell,
		bookBash,
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

func parseFlags() {
	flag.StringVar(&flgAnalytics, "analytics", "", "google analytics code")
	flag.BoolVar(&flgPreview, "preview", false, "if true will start watching for file changes and re-build everything")
	flag.BoolVar(&flgAllBooks, "all-books", false, "if true will do all books")
	flag.BoolVar(&flgUpdateOutput, "update-output", false, "if true, will update ouput files in cache")
	flag.BoolVar(&flgNoCache, "no-cache", false, "if true, disables cache for notion")
	flag.BoolVar(&flgVerbose, "verbose", false, "if true will log more")
	flag.StringVar(&flgRedownloadOne, "redownload-one", "", "notion id of a page to re-download")
	flag.StringVar(&flgRebuildOnePage, "rebuild-one", "", "notion id of a page to re-build")
	flag.BoolVar(&flgRedownloadReplit, "redownload-replit", false, "if true, redownloads replits")
	flag.StringVar(&flgRedownloadOneReplit, "redownload-one-replit", "", "replit url and book to download")
	flag.StringVar(&flgRedownloadBook, "redownload-book", "", "redownload a book")
	flag.Parse()

	if flgRedownloadOne != "" {
		flgRedownloadOne = extractNotionIDFromURL(flgRedownloadOne)
	}
	if flgRebuildOnePage != "" {
		flgRebuildOnePage = extractNotionIDFromURL(flgRebuildOnePage)
	}

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

	notionAuthToken = os.Getenv("NOTION_TOKEN")
	if notionAuthToken != "" {
		fmt.Printf("NOTION_TOKEN provided, can write back\n")
	} else {
		fmt.Printf("NOTION_TOKEN not provided, read only\n")
	}
}

func downloadBook(c *notionapi.Client, book *Book) {
	notionStartPageID := book.NotionStartPageID
	book.pageIDToPage = map[string]*notionapi.Page{}
	fmt.Printf("Loading %s...", book.Title)
	loadNotionPages(book, c, notionStartPageID, book.pageIDToPage, !flgNoCache)
	fmt.Printf(" got %d pages\n", len(book.pageIDToPage))
	bookFromPages(book)
}

func loadSOUserMappingsMust() {
	path := filepath.Join("stack-overflow-docs-dump", "users.json.gz")
	err := common.JSONDecodeGzipped(path, &soUserIDToNameMap)
	u.PanicIfErr(err)
}

// TODO: probably more
func getDefaultLangForBook(bookName string) string {
	s := strings.ToLower(bookName)
	switch s {
	case "go":
		return "go"
	case "android":
		return "java"
	case "ios":
		return "ObjectiveC"
	case "microsoft sql server":
		return "sql"
	case "node.js":
		return "javascript"
	case "mysql":
		return "sql"
	case ".net framework":
		return "c#"
	}
	return s
}

func shouldCopyImage(path string) bool {
	return !strings.Contains(path, "@2x")
}

func copyCoversMust() {
	copyFilesRecur(filepath.Join("www", "covers"), "covers", shouldCopyImage)
}

func getAlmostMaxProcs() int {
	// leave some juice for other programs
	nProcs := runtime.NumCPU() - 2
	if nProcs < 1 {
		return 1
	}
	return nProcs
}

// copy from tmpl to www, optimize if possible, add
// sha1 of the content as part of the name
func copyToWwwAsSha1MaybeMust(srcName string) {
	var dstPtr *string
	minifyType := ""
	switch srcName {
	case "main.css":
		dstPtr = &pathMainCSS
		minifyType = "text/css"
	case "app.js":
		dstPtr = &pathAppJS
		minifyType = "text/javascript"
	case "favicon.ico":
		dstPtr = &pathFaviconICO
	default:
		panicIf(true, "unknown srcName '%s'", srcName)
	}
	src := filepath.Join("tmpl", srcName)
	d, err := ioutil.ReadFile(src)
	panicIfErr(err)

	if doMinify && minifyType != "" {
		d2, err := minifier.Bytes(minifyType, d)
		maybePanicIfErr(err)
		if err == nil {
			fmt.Printf("Compressed %s from %d => %d (saved %d)\n", srcName, len(d), len(d2), len(d)-len(d2))
			d = d2
		}
	}

	sha1Hex := u.Sha1HexOfBytes(d)
	name := nameToSha1Name(srcName, sha1Hex)
	dst := filepath.Join("www", "s", name)
	err = ioutil.WriteFile(dst, d, 0644)
	panicIfErr(err)
	*dstPtr = filepath.ToSlash(dst[len("www"):])
	fmt.Printf("Copied %s => %s\n", src, dst)
}

func genBooks(books []*Book) {
	nProcs := getAlmostMaxProcs()

	timeStart := time.Now()
	clearSitemapURLS()
	copyCoversMust()

	copyToWwwAsSha1MaybeMust("main.css")
	copyToWwwAsSha1MaybeMust("app.js")
	copyToWwwAsSha1MaybeMust("favicon.ico")
	genIndex(books)
	genIndexGrid(books)
	gen404TopLevel()
	genAbout()
	genFeedback()

	for _, book := range books {
		genBook(book)
	}
	writeSitemap()
	fmt.Printf("Used %d procs, finished generating all books in %s\n", nProcs, time.Since(timeStart))
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
	doMinify = !flgPreview
}

func isNotionCachedInDir(dir string, id string) bool {
	id = normalizeID(id)
	files, err := ioutil.ReadDir(dir)
	panicIfErr(err)
	for _, fi := range files {
		name := fi.Name()
		if strings.HasPrefix(name, id) {
			return true
		}
	}
	return false
}

func findBookFromCachedPageID(id string) *Book {
	files, err := ioutil.ReadDir("cache")
	panicIfErr(err)
	for _, book := range allBooks {
		if book.NotionStartPageID == id {
			return book
		}
	}

	for _, fi := range files {
		if !fi.IsDir() {
			continue
		}
		dir := fi.Name()
		book := findBook(dir)
		panicIf(book == nil, "didn't find book for dir '%s'", dir)
		if isNotionCachedInDir(filepath.Join("cache", dir, "notion"), id) {
			return book
		}
	}
	return nil
}

func isReplitURL(uri string) bool {
	return strings.Contains(uri, "repl.it/")
}

func redownloadOneReplit() {
	if len(flag.Args()) != 1 {
		fmt.Printf("-redownload-one-replit expects 2 arguments: book and replit url\n")
		os.Exit(1)
	}
	uri := flgRedownloadOneReplit
	bookName := flag.Args()[0]
	if !isReplitURL(uri) {
		panicIf(!isReplitURL(bookName), "neither '%s' nor '%s' look like repl.it url", uri, bookName)
		uri, bookName = bookName, uri
	}
	book := findBook(bookName)
	panicIf(book == nil, "'%s' is not a valid book name", bookName)
	initBook(book)
	_, isNew, err := downloadAndCacheReplit(book.replitCache, uri)
	panicIfErr(err)
	fmt.Printf("genReplitEmbed: downloaded %s,  isNew: %v\n", uri+".zip", isNew)
}

func initBook(book *Book) {
	var err error

	createDirMust(book.OutputCacheDir())
	createDirMust(book.NotionCacheDir())

	reloadCachedOutputFilesMust(book)
	path := filepath.Join(book.OutputCacheDir(), "sha1_to_go_playground_id.txt")
	book.sha1ToGoPlaygroundCache = readSha1ToGoPlaygroundCache(path)
	path = filepath.Join(book.OutputCacheDir(), "sha1_to_glot_playground_id.txt")
	book.sha1ToGlotPlaygroundCache = readSha1ToGlotPlaygroundCache(path)
	book.replitCache, err = LoadReplitCache(book.ReplitCachePath())
	panicIfErr(err)
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

func redownloadBook(id string) {
	// when redownloading we also want to update ouput
	flgUpdateOutput = true
	book := findBook(id)
	if book == nil {
		fmt.Printf("Didn't find a book with id '%s'\n", id)
		os.Exit(1)
	}
	flgNoCache = true
	client := &notionapi.Client{
		AuthToken: notionAuthToken,
	}
	initBook(book)
	downloadBook(client, book)
}

func main() {
	if false {
		glotRunTestAndExit()
	}
	if false {
		glotGetSnippedIDTestAndExit()
	}

	parseFlags()

	if flgRedownloadOneReplit != "" {
		redownloadOneReplit()
		os.Exit(0)
	}

	if false {
		// only needs to be run when we add new covers
		genTwitterImagesAndExit()
	}

	os.RemoveAll("www")
	createDirMust(filepath.Join("www", "s"))
	createDirMust("log")

	if flgRedownloadBook != "" {
		redownloadBook(flgRedownloadBook)
		return
	}

	initMinify()
	loadSOUserMappingsMust()

	client := &notionapi.Client{
		AuthToken: notionAuthToken,
	}

	if flgRebuildOnePage != "" {
		book := findBookFromCachedPageID(flgRebuildOnePage)
		if book == nil {
			fmt.Printf("didn't find book for id %s\n", flgRebuildOnePage)
			os.Exit(1)
		}
		fmt.Printf("Rebuilding %s for book %s\n", flgRebuildOnePage, book.Dir)
		page := loadPageFromCache(book, flgRebuildOnePage)
		flgNoCache = false
		initBook(book)
		downloadBook(client, book)
		loadSoContributorsMust(book)
		genOnePage(book, page.ID)
		os.Exit(0)
	}

	books := booksMain
	if flgAllBooks {
		books = allBooks
	}

	if flgRedownloadOne != "" {
		book := findBookFromCachedPageID(flgRedownloadOne)
		if book == nil {
			fmt.Printf("didn't find book for id %s\n", flgRedownloadOne)
			os.Exit(1)
		}
		fmt.Printf("Downloading %s for book %s\n", flgRedownloadOne, book.Dir)
		// download a single page from notion and re-generate content
		page, err := downloadAndCachePage(book, client, flgRedownloadOne)
		if err != nil {
			fmt.Printf("downloadAndCachePage of '%s' failed with %s\n", flgRedownloadOne, err)
			os.Exit(1)
		}
		flgNoCache = false
		initBook(book)
		downloadBook(client, book)
		loadSoContributorsMust(book)
		genOnePage(book, page.ID)
		flgPreview = true
		books = []*Book{book}
		// and fallthrough to re-generate books
	}

	if flgPreview {
		if len(flag.Args()) > 0 {
			var newBooks []*Book
			for _, name := range flag.Args() {
				book := findBook(name)
				if book == nil {
					fmt.Printf("Didn't find book named '%s'\n", name)
					continue
				}
				newBooks = append(newBooks, book)
			}
			if len(newBooks) > 0 {
				books = newBooks
			}
		}
	}

	for _, book := range books {
		initBook(book)
		downloadBook(client, book)
		loadSoContributorsMust(book)
	}

	genBooks(books)
	genNetlifyHeaders()
	genNetlifyRedirects(books)
	printAndClearErrors()

	if flgPreview {
		startPreview()
	}
}
