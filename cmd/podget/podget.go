// A simple podcast downloader.
//
// I use this to download a copy of This American Life and file it away for
// safekeeping in my archive.
//
// Example:
//   podget -d ~/TAL -r 30 -v http://feed.thisamericanlife.org/talpodcast
//
// The -r 30 means that if a file exists already but is more than 30 days
// old, we assume they're doing a rerun and download the new version.
//
package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lpar/podtools/podcast"
)

// Max number of downloads to queue
const queueSize = 15

func logInfo(msg string, vals ...interface{}) {
	if *verbose {
		fmt.Printf(msg+"\n", vals...)
	}
}

func logDebug(msg string, vals ...interface{}) {
	if *debug {
		fmt.Printf(msg+"\n", vals...)
	}
}

func logError(msg string, vals ...interface{}) {
	fmt.Fprintf(os.Stderr, msg+"\n", vals...)
}

type Download struct {
	URL  string
	File string
	Item *podcast.Item
}

var dlqueue = make(chan *Download, queueSize)

func downloader() {
	logDebug("download task starting")
	for dl := range dlqueue {
		download(dl.URL, dl.File, dl.Item)
		time.Sleep(2 * time.Second)
	}
	logDebug("all downloads complete, download task finishing")
}

func download(fromurl string, tofile string, item *podcast.Item) {
	logDebug("beginning download %s -> %s", fromurl, tofile)
	dir := path.Dir(tofile)
	err := os.MkdirAll(dir, 0777)
	if err != nil {
		logError("can't create destination directory %s: %v", dir)
		return
	}
	fout, err := os.Create(tofile)
	if err != nil {
		logError("can't create %s: %v", tofile, err)
		return
	}
	defer fout.Close()
	resp, err := http.Get(fromurl)
	if err != nil {
		logError("can't download %s: %v", fromurl, err)
		return
	}
	defer resp.Body.Close()
	n, err := io.Copy(fout, resp.Body)
	if err != nil {
		logError("error downloading %s: %v", fromurl, err)
		return
	}
	logInfo("%d bytes downloaded to %s", n, tofile)
	logDebug("ending download %s -> %s", fromurl, tofile)

	coverjpg := tofile + ".jpg"

	err = DownloadFile(coverjpg, item.Image.Href) //download artwork associated with the podcast episode
	if err != nil {
		panic(err)
	}
	thedate := item.PubDate.Format("2006-01-02")
	mp3orm4aextension := path.Ext(path.Base(tofile))
	//created this to avoid tagging m4as using MP3 ID3 data, incorrect tagging will render files useless
	// only MP3 & M4A supported for now
	// more support can be easily added
	if mp3orm4aextension == ".mp3" {
		output, err := exec.Command("mid3v2", "-v", "--album", item.ChannelTitle, "--TPOS", "1", "--TPE2", item.ChannelTitle, "--genre", "Podcast", "--artist", item.ChannelTitle, "--song", item.Title, "--year", item.PubDate.Format("2006-01-02"), tofile).CombinedOutput()
		// being lazy here, you'd want this to be an optional dependancy passed through a variable like -mid3v2, you can also remove the -v tag if you'd like
		// this command also initiates creates an ID3v2 tag for files that don't have one to start off with - No ID3 header found; creating a new tag
		// mid3v2 doesn't like writing comments that have : in them or other special characters
		// mid3v2 will also overwrite any images currently embeded in the audio file
		if err != nil {
			os.Stderr.WriteString(err.Error())
		}
		fmt.Println(string(output))

		_, err = exec.Command("eyeD3", "--comment", item.Description, "--add-image", coverjpg+":FRONT_COVER:\"2\"", tofile).CombinedOutput()
		// I haven't been able to debug this but eyeD3 works well for --comments
		// eyeD3 accepts all comments including special characters, eyeD3 doesn't like image file paths that contain : or other special characters
		// eyeD3 does not overwrite image files if its "DESCRIPTION" tag is different from the currently embedded ones
		// eyeD3 also does not like the item.PubDate.Format("2006-01-02") format for some reason
		// I didn't wanna bother debugging why this is happening so I'm using both packagaes to get the job done
		if err != nil {
			os.Stderr.WriteString(err.Error())
		}

		output, err = exec.Command("eyeD3", tofile).CombinedOutput()
		// this is a verbose output to show the brand new ID3V2 tag
		if err != nil {
			os.Stderr.WriteString(err.Error())
		}
		fmt.Println(string(output)) //this output may be a better alternative to the standard -v output of the podtools program
	} else if mp3orm4aextension == ".m4a" {
		output, err := exec.Command("tageditor", "set", "title="+item.Title, "album="+item.ChannelTitle, "albumartist="+item.ChannelTitle, "disk=1", "genre=Podcast", "year="+thedate, "comment="+item.Description, "cover="+coverjpg, "-f", tofile).CombinedOutput()
		// being lazy here, you'd want this to be an optional dependancy passed through a variable like -mid3v2, you can also remove the -v tag if you'd like
		// this command also initiates creates an ID3v2 tag for files that don't have one to start off with - No ID3 header found; creating a new tag
		// mid3v2 doesn't like writing comments that have : in them or other special characters
		// mid3v2 will also overwrite any images currently embeded in the audio file
		if err != nil {
			os.Stderr.WriteString(err.Error())
		}
		fmt.Println(string(output))

		e := os.Remove(tofile + ".bak") //delete the downloaded item artwork
		if e != nil {
			log.Fatal(e)
		}
		// output, err = exec.Command("sudo", "AtomicParsley", tofile, "--description", item.Description, "--artwork", coverjpg+":FRONT_COVER:\"2\"").CombinedOutput()
		// // being lazy here, you'd want this to be an optional dependancy passed through a variable like -mid3v2, you can also remove the -v tag if you'd like
		// // this command also initiates creates an ID3v2 tag for files that don't have one to start off with - No ID3 header found; creating a new tag
		// // mid3v2 doesn't like writing comments that have : in them or other special characters
		// // mid3v2 will also overwrite any images currently embeded in the audio file
		// if err != nil {
		// 	os.Stderr.WriteString(err.Error())
		// }
		// fmt.Println(string(output))

	} else if mp3orm4aextension == ".aac" {
		output, err := exec.Command("tageditor", "set", "title="+item.Title, "album="+item.ChannelTitle, "albumartist="+item.ChannelTitle, "disk=1", "genre=Podcast", "year="+thedate, "comment="+item.Description, "cover="+coverjpg, "-f", tofile).CombinedOutput()
		if err != nil {
			os.Stderr.WriteString(err.Error())
		}
		fmt.Println(string(output))

		e := os.Remove(tofile + ".bak") //delete the backup file
		if e != nil {
			log.Fatal(e)
		}

	} else if mp3orm4aextension == ".ogg" {
		output, err := exec.Command("tageditor", "set", "title="+item.Title, "album="+item.ChannelTitle, "albumartist="+item.ChannelTitle, "disk=1", "genre=Podcast", "year="+thedate, "comment="+item.Description, "cover="+coverjpg, "-f", tofile).CombinedOutput()
		if err != nil {
			os.Stderr.WriteString(err.Error())
		}
		fmt.Println(string(output))

		e := os.Remove(tofile + ".bak") //delete the backup file
		if e != nil {
			log.Fatal(e)
		}

	} else if mp3orm4aextension == ".wav" {
		output, err := exec.Command("tageditor", "set", "title="+item.Title, "album="+item.ChannelTitle, "albumartist="+item.ChannelTitle, "disk=1", "genre=Podcast", "year="+thedate, "comment="+item.Description, "cover="+coverjpg, "-f", tofile).CombinedOutput()
		if err != nil {
			os.Stderr.WriteString(err.Error())
		}
		fmt.Println(string(output))

		e := os.Remove(tofile + ".bak") //delete the backup file
		if e != nil {
			log.Fatal(e)
		}

	} else if mp3orm4aextension == ".wmv" {
		output, err := exec.Command("tageditor", "set", "title="+item.Title, "album="+item.ChannelTitle, "albumartist="+item.ChannelTitle, "disk=1", "genre=Podcast", "year="+thedate, "comment="+item.Description, "cover="+coverjpg, "-f", tofile).CombinedOutput()
		if err != nil {
			os.Stderr.WriteString(err.Error())
		}
		fmt.Println(string(output))

		e := os.Remove(tofile + ".bak") //delete the backup file
		if e != nil {
			log.Fatal(e)
		}

	} else if mp3orm4aextension == ".flac" {
		output, err := exec.Command("tageditor", "set", "title="+item.Title, "album="+item.ChannelTitle, "albumartist="+item.ChannelTitle, "disk=1", "genre=Podcast", "year="+thedate, "comment="+item.Description, "cover="+coverjpg, "-f", tofile).CombinedOutput()
		if err != nil {
			os.Stderr.WriteString(err.Error())
		}
		fmt.Println(string(output))

		e := os.Remove(tofile + ".bak") //delete the backup file
		if e != nil {
			log.Fatal(e)
		}

	} else {
		fmt.Println("unsupported audio container: " + mp3orm4aextension)
	}

	e := os.Remove(coverjpg) //delete the downloaded item artwork
	if e != nil {
		log.Fatal(e)
	}

}

func DownloadFile(filepath string, url string) error {

	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
}

var asciiOnly = regexp.MustCompile("[[:^ascii:]]")

func processChannel(rss []byte) error {
	logDebug("processing channel data [%s]", string(rss[0:40]))
	var feed podcast.RSS
	err := xml.Unmarshal(rss, &feed)
	if err != nil {
		return fmt.Errorf("error parsing XML: %v", err)
	}
	channel := feed.Channel
	name := asciiOnly.ReplaceAllLiteralString(channel.Title, "")
	dir := strings.Replace(name, " ", "_", -1)
	logInfo("%s %s/", channel.Title, dir)
	for _, item := range channel.Item {
		logDebug("processing item")
		processItem(channel.Title, dir, item)
		item.ChannelTitle = channel.Title
	}
	logDebug("done processing channel data")
	return nil
}

func processItem(feedtitle string, feeddir string, item *podcast.Item) {
	enc := item.Enclosure
	logInfo("  %v %s %v", item.PubDate.Format("2006-01-02"), item.Title, item.Duration.String())
	u, err := url.Parse(enc.URL)
	if err != nil {
		logError("can't parse URL %s for %s: %v", enc.URL, feedtitle, err)
		return
	}
	var destfile string
	if *podtrac != "" {
		destfile, err = depodtracify(item, enc, u, filepath.Ext(u.Path))
		if err != nil {
			logError("skipping episode: %v", err)
			return
		}
		destfile = filepath.Join(*destdir, feeddir, destfile)
	} else {

		reg, err := regexp.Compile("[^A-Za-z0-9_. ]+")
		if err != nil {
			log.Fatal(err)
		}

		ogpodcastfilename, _ := http.NewRequest("GET", enc.URL, nil)            //GET the server podcast filename from the enc.URL
		podcastfileextension := path.Ext(path.Base(ogpodcastfilename.URL.Path)) //get the server podcast file extension

		podcastfilename := reg.ReplaceAllString(item.Title, "")
		// item.Title = reg.ReplaceAllString(item.Title, "")
		filenamearray := []string{item.PubDate.Format("2006-01-02"), " - ", podcastfilename, podcastfileextension}
		destfile = filepath.Join(*destdir, feeddir, filepath.Base(strings.Join(filenamearray, "")))
	}
	stats, err := os.Stat(destfile)
	overwrite := false
	if err == nil && *maxdays > 0 {
		maxage := time.Duration(*maxdays) * time.Hour * 24
		age := time.Since(stats.ModTime()).Round(time.Second)
		overwrite = age > maxage
		fw := "not "
		if overwrite {
			fw = ""
		}
		logInfo("%sallowing overwrite of %s, file is %v old", fw, destfile, age)
	}
	if os.IsNotExist(err) || overwrite {
		dlqueue <- &Download{URL: enc.URL, File: destfile, Item: item}
		return
	}
	logError("skipping %s, already downloaded", destfile)
}

// depodtracify handles extracting an episode number from the data, in cases where the podcast
// is using podtrac. Otherwise, every episode ends up with the same filename `default.mp3`.
func depodtracify(item *podcast.Item, enc *podcast.Enclosure, u *url.URL, ext string) (string, error) {
	data := make(map[string]string)
	data["item.author"] = item.Author
	data["item.category"] = item.Category
	data["item.description"] = item.Description
	data["item.duration"] = item.Duration.String()
	data["item.guid"] = item.Guid.Text
	data["item.pubDate"] = item.PubDate.String()
	data["item.title"] = item.Title
	data["enclosure.url"] = enc.URL
	data["url"] = u.String()
	x := data[podtracField]
	ep := podtracRE.FindStringSubmatch(x)
	if len(ep) < 1 || ep[1] == "" {
		if *debug {
			logDebug("search data: %s", x)
			logDebug("     regexp: %s", podtracRE)
		}
		return "", fmt.Errorf("failed to extract filename for %s", u.String())
	}
	return ep[1] + ext, nil
}

var verbose = flag.Bool("v", false, "verbose output")
var debug = flag.Bool("debug", false, "debug mode")
var destdir = flag.String("d", "", "destination directory")
var maxdays = flag.Int("r", 0, "enable rerun processing after specified number of days")
var podtrac = flag.String("podtrac", "", "how to extract episode number, see README")

var podtracRE *regexp.Regexp
var podtracField string

func processFeed(feedurl string) {
	resp, err := http.Get(feedurl)
	if err != nil {
		logError("can't fetch feed %s: %v", feedurl, err)
		return
	}
	defer resp.Body.Close()
	xmlb, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logError("error reading response from %s: %v", feedurl, err)
		return
	}
	err = processChannel(xmlb)
	if err != nil {
		logError("can't process %s: %v", feedurl, err)
	}
}

func podtracCompile() error {
	var err error
	instruction := *podtrac
	if instruction == "" {
		return nil
	}
	chunks := strings.SplitN(instruction, " ", 2)
	podtracField = strings.TrimSpace(chunks[0])
	sregex := strings.Trim(chunks[1], " /")
	if *debug {
		logDebug("compiling %s", sregex)
	}
	podtracRE, err = regexp.Compile(sregex)
	return err
}

func main() {
	flag.Parse()

	if err := podtracCompile(); err != nil {
		logError("can't compile podtrac decode instruction: %v", err)
		os.Exit(1)
	} else {
		logDebug("will search field %s for %s", podtracField, podtracRE)
	}

	wg := new(sync.WaitGroup)

	wg.Add(1)
	go func() {
		defer wg.Done()
		downloader()
	}()

	wg.Add(1)
	go func() {
		for _, feedurl := range flag.Args() {
			logInfo("fetching %s", feedurl)
			defer wg.Done()
			processFeed(feedurl)
		}
		close(dlqueue)
	}()
	wg.Wait()

}
