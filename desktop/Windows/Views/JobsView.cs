using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using System.Text.Json.Nodes;
using Reconciliation.Windows.ViewModels;

namespace Reconciliation.Windows.Views;
public sealed class JobsView : UserControl
{
    readonly AppViewModel vm;
    readonly StackPanel panel = UI.Stack();
    readonly ContentControl progress = UI.Host();
    readonly TextBox id = UI.Field("任务标识");
    CancellationTokenSource? polling;
    public JobsView(AppViewModel model)
    {
        vm = model;
        id.Text = vm.CurrentJob.S("id");
        id.FontFamily = (Microsoft.UI.Xaml.Media.FontFamily)Application.Current.Resources["LedgerMonoFont"];
        var scope = new RadioButtons
        {
            Header = "数据范围"
        };
        foreach (var(key, label)in new[]
        {
            ("all", "两个年度全部（正式范围）"),
            ("year1", "首年度全部"),
            ("benchmark", "首表固定一万行（基准测试）")
        }

        )
            scope.Items.Add(new RadioButton { Content = label, Tag = key });
        scope.SelectedIndex = 0;
        var create = UI.Primary(vm, "创建任务", async () =>
        {
            var job = await vm.Api.Send("jobs", new { scope = (scope.SelectedItem as RadioButton)?.Tag as string ?? "all" });
            vm.CurrentJob = job;
            id.Text = job.S("id");
            StartPoll();
        }, Glyph.Play);
        var resume = UI.Button(vm, "恢复进度查询", () =>
        {
            StartPoll();
            return Task.CompletedTask;
        }, Glyph.Refresh);
        resume.VerticalAlignment = VerticalAlignment.Bottom;
        var resumeRow = new Grid
        {
            ColumnSpacing = 12
        };
        resumeRow.ColumnDefinitions.Add(new() { Width = new GridLength(1, GridUnitType.Star) });
        resumeRow.ColumnDefinitions.Add(new() { Width = GridLength.Auto });
        Grid.SetColumn(resume, 1);
        resumeRow.Add(id, resume);
        panel.Spacing = 16;
        panel.Add(UI.PageHeader("数据任务", "准备官方数据、导入和对账在 Go 后端执行；任务进行中仍可检索已发布运行。"), UI.Split(UI.Section("创建数据任务", UI.Stack(scope, create), "完成后发布为新的数据运行，业务页面可在运行选择中切换。"), UI.Section("恢复进度查询", resumeRow, "关闭页面不会中止任务；用任务标识重新查看进度。"), 900, 1.2), progress);
        panel.Padding = new Thickness(0, 0, 12, 24);
        Content = UI.Scroll(UI.Column(panel, 1100));
        Loaded += (_, _) =>
        {
            if (id.Text != "")
                StartPoll();
        };
        Unloaded += (_, _) =>
        {
            polling?.Cancel();
            polling?.Dispose();
            polling = null;
        };
    }

    void Show(JsonNode job)
    {
        var state = job.S("status");
        bool active = state is "queued" or "running";
        var head = UI.Flow(UI.Status(state), UI.Text(job.S("stage")), UI.Mono(job.S("id"), 13));
        head.Spacing = 12;
        var p = UI.Stack(head);
        if (active)
            p.Children.Add(new ProgressBar { IsIndeterminate = true });
        if (job.S("error") != "")
            p.Children.Add(new InfoBar { IsOpen = true, IsClosable = false, Severity = InfoBarSeverity.Error, Title = "任务未完成", Message = job.S("error") });
        p.Children.Add(UI.Details(job["progress"]));
        progress.Content = UI.Section("任务进度", p, active ? "每 1.2 秒自动刷新。" : null);
    }

    void StartPoll()
    {
        polling?.Cancel();
        polling = CancellationTokenSource.CreateLinkedTokenSource(vm.Token);
        _ = vm.Guard(async () =>
        {
            var ct = polling.Token;
            while (!ct.IsCancellationRequested)
            {
                var gen = vm.Generation;
                var job = await vm.Api.Send("jobs/" + Uri.EscapeDataString(id.Text), ct: ct);
                if (gen != vm.Generation)
                    return;
                vm.CurrentJob = job;
                Show(job);
                if (job.S("status")is "succeeded" or "failed" or "interrupted")
                {
                    if (job.S("status") == "succeeded")
                    {
                        await vm.LoadRuns();
                        vm.RunID = job.S("run_id");
                        vm.Message("任务已发布，业务页面已可选择新运行。");
                    }

                    break;
                }

                await Task.Delay(1200, ct);
            }
        });
    }
}
