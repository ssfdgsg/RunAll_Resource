package data

import (
	"context"
	"fmt"
	"io"
	"resource/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

type execRepo struct {
	client *kubernetes.Clientset
	config *rest.Config
	log    *log.Helper
}

// NewExecRepo 创建 exec 仓储实现
func NewExecRepo(k8sClient *K8sClient, logger log.Logger) biz.ExecRepo {
	return &execRepo{
		client: k8sClient.Client,
		config: k8sClient.Config,
		log:    log.NewHelper(logger),
	}
}

// StreamExec 流式执行容器命令
func (r *execRepo) StreamExec(ctx context.Context, opts biz.ExecOptions, input <-chan biz.ExecInput, output chan<- biz.ExecOutput) error {
	// 1. 通过 label selector 查找 Pod
	podList, err := r.client.CoreV1().Pods(opts.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("instance-id=%s,app=instance", opts.InstanceID),
		Limit:         1,
	})
	if err != nil {
		r.log.Errorf("failed to list pods: %v", err)
		return fmt.Errorf("failed to list pods: %w", err)
	}

	if len(podList.Items) == 0 {
		r.log.Errorf("no pod found for instance %s in namespace %s", opts.InstanceID, opts.Namespace)
		return fmt.Errorf("pod not found for instance %s", opts.InstanceID)
	}

	podName := podList.Items[0].Name

	// 2. 构建 exec 请求
	req := r.client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(opts.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: opts.ContainerName,
			Command:   opts.Command,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       opts.TTY,
		}, scheme.ParameterCodec)

	// 3. 创建 SPDY 执行器
	exec, err := remotecommand.NewSPDYExecutor(r.config, "POST", req.URL())
	if err != nil {
		r.log.Errorf("failed to create SPDY executor: %v", err)
		return err
	}

	// 4. 创建流适配器
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()

	// 5. 创建终端大小队列（用于 resize）
	sizeQueue := &terminalSizeQueue{
		resizeChan: make(chan remotecommand.TerminalSize, 10),
	}

	// 6. 启动输入处理协程
	go r.handleInput(input, stdinWriter, sizeQueue)

	// 7. 启动输出处理协程
	go r.handleOutput(stdoutReader, "stdout", output)
	go r.handleOutput(stderrReader, "stderr", output)

	// 8. 执行命令
	streamOpts := remotecommand.StreamOptions{
		Stdin:  stdinReader,
		Stdout: stdoutWriter,
		Stderr: stderrWriter,
		Tty:    opts.TTY,
	}

	// 如果启用 TTY，添加终端大小队列
	if opts.TTY {
		streamOpts.TerminalSizeQueue = sizeQueue
	}

	err = exec.StreamWithContext(ctx, streamOpts)

	// 9. 处理执行结果
	if err != nil {
		r.log.Errorf("exec stream error: %v", err)
		output <- biz.ExecOutput{
			Type: biz.ExecOutputError,
			Data: []byte(err.Error()),
		}
	}

	// 10. 发送退出信号
	exitCode := int32(0)
	if err != nil {
		exitCode = 1
	}
	output <- biz.ExecOutput{
		Type:     biz.ExecOutputExit,
		ExitCode: exitCode,
	}

	return err
}

// handleInput 处理输入流
func (r *execRepo) handleInput(input <-chan biz.ExecInput, writer *io.PipeWriter, sizeQueue *terminalSizeQueue) {
	defer writer.Close()
	defer close(sizeQueue.resizeChan)

	for in := range input {
		switch in.Type {
		case biz.ExecInputStdin:
			// 写入标准输入
			if _, err := writer.Write(in.Data); err != nil {
				r.log.Errorf("failed to write stdin: %v", err)
				return
			}

		case biz.ExecInputResize:
			// 发送终端大小调整
			sizeQueue.resizeChan <- remotecommand.TerminalSize{
				Width:  uint16(in.Cols),
				Height: uint16(in.Rows),
			}
			r.log.Debugf("terminal resized to %dx%d", in.Cols, in.Rows)
		}
	}
}

// handleOutput 处理输出流
func (r *execRepo) handleOutput(reader *io.PipeReader, stream string, output chan<- biz.ExecOutput) {
	defer reader.Close()

	buf := make([]byte, 8192)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			// 复制数据，避免缓冲区被覆盖
			data := make([]byte, n)
			copy(data, buf[:n])

			output <- biz.ExecOutput{
				Type:   biz.ExecOutputData,
				Stream: stream,
				Data:   data,
			}
		}

		if err != nil {
			if err != io.EOF {
				r.log.Errorf("error reading %s: %v", stream, err)
			}
			break
		}
	}
}

// terminalSizeQueue 实现 remotecommand.TerminalSizeQueue 接口
type terminalSizeQueue struct {
	resizeChan chan remotecommand.TerminalSize
}

// Next 返回下一个终端大小
func (t *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	size, ok := <-t.resizeChan
	if !ok {
		return nil
	}
	return &size
}
