package up

import (
	"context"
	"fmt"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/iyear/tdl/pkg/texpr"
	"os"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/spf13/viper"
	"go.uber.org/multierr"

	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/dcpool"
	"github.com/iyear/tdl/pkg/kv"
	"github.com/iyear/tdl/pkg/prog"
	"github.com/iyear/tdl/pkg/storage"
	"github.com/iyear/tdl/pkg/tclient"
	"github.com/iyear/tdl/pkg/uploader"
	"github.com/iyear/tdl/pkg/utils"
)

type Options struct {
	Chat     string
	Thread   int
	To       string
	Paths    []string
	Includes []string
	Excludes []string
	Remove   bool
	Photo    bool
	Caption  string
}

func Run(ctx context.Context, c *telegram.Client, kvd kv.KV, opts Options) (rerr error) {
	if opts.To == "-" || opts.Caption == "-" {
		fg := texpr.NewFieldsGetter(nil)

		fields, err := fg.Walk(exprEnv(nil, nil))
		if err != nil {
			return fmt.Errorf("failed to walk fields: %w", err)
		}

		fmt.Print(fg.Sprint(fields, true))
		return nil
	}
	files, err := walk(opts.Paths, opts.Includes, opts.Excludes)
	if err != nil {
		return err
	}

	color.Blue("Files count: %d", len(files))

	pool := dcpool.NewPool(c,
		int64(viper.GetInt(consts.FlagPoolSize)),
		tclient.NewDefaultMiddlewares(ctx, viper.GetDuration(consts.FlagReconnectTimeout))...)
	defer multierr.AppendInvoke(&rerr, multierr.Close(pool))

	manager := peers.Options{Storage: storage.NewPeers(kvd)}.Build(pool.Default(ctx))

	to, err := resolveDest(ctx, manager, opts.To)
	if err != nil {
		return errors.Wrap(err, "get target peer")
	}

	caption, err := resolveCaption(ctx, opts.Caption)
	if err != nil {
		return errors.Wrap(err, "get caption")
	}

	upProgress := prog.New(utils.Byte.FormatBinaryBytes)
	upProgress.SetNumTrackersExpected(len(files))
	prog.EnablePS(ctx, upProgress)

	options := uploader.Options{
		Client:   pool.Default(ctx),
		PartSize: viper.GetInt(consts.FlagPartSize),
		Threads:  viper.GetInt(consts.FlagThreads),
		Iter:     newIter(files, to, caption, opts.Chat, opts.Thread, opts.Photo, opts.Remove, manager),
		Progress: newProgress(upProgress),
		Delay:    viper.GetDuration(consts.FlagDelay),
	}

	up := uploader.New(options)

	go upProgress.Render()
	defer prog.Wait(ctx, upProgress)

	return up.Upload(ctx, viper.GetInt(consts.FlagLimit))
}

//func resolveDestPeer(ctx context.Context, manager *peers.Manager, chat string) (peers.Peer, error) {
//	if chat == "" {
//		return manager.FromInputPeer(ctx, &tg.InputPeerSelf{})
//	}
//
//	return utils.Telegram.GetInputPeer(ctx, manager, chat)
//}

// resolveDest parses the input string and returns a vm.Program. It can be a CHAT, a text or a file based on expression engine.
func resolveDest(ctx context.Context, manager *peers.Manager, input string) (*vm.Program, error) {
	compile := func(i string) (*vm.Program, error) {
		// we pass empty peer and message to enable type checking
		return expr.Compile(i, expr.Env(exprEnv(nil, nil)))
	}

	// default
	if input == "" {
		return compile(`""`)
	}

	// file
	if exp, err := os.ReadFile(input); err == nil {
		return compile(string(exp))
	}

	// chat
	if _, err := utils.Telegram.GetInputPeer(ctx, manager, input); err == nil {
		// convert to const string
		return compile(fmt.Sprintf(`"%s"`, input))
	}

	// text
	return compile(input)
}

func resolveCaption(ctx context.Context, input string) (*vm.Program, error) {
	compile := func(i string) (*vm.Program, error) {
		// we pass empty peer and message to enable type checking
		return expr.Compile(i, expr.Env(exprEnv(nil, nil)))
	}

	// default
	if input == "" {
		return compile(`""`)
	}

	// file
	if exp, err := os.ReadFile(input); err == nil {
		return compile(string(exp))
	}

	// text
	return compile(input)
}
