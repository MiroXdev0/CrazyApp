using System;
using System.Diagnostics.CodeAnalysis;
using Avalonia.Controls;
using Avalonia.Controls.Templates;
using Nodren.Desktop.ViewModels;

namespace Nodren.Desktop;

/// <summary>
/// Resolves a view for a given view model.
/// </summary>
[RequiresUnreferencedCode(
"The default ViewLocator uses reflection, which may be removed during trimming.",
Url = "https://docs.avaloniaui.net/docs/concepts/view-locator")]
public sealed class ViewLocator : IDataTemplate
{
public Control? Build(object? param)
{
if (param is null)
return null;

    var viewModelName = param.GetType().FullName;

    if (viewModelName is null)
        return null;

    var viewName = viewModelName.Replace(
        "ViewModel",
        "View",
        StringComparison.Ordinal
    );

    var viewType = Type.GetType(viewName);

    if (viewType is not null)
    {
        return (Control)Activator.CreateInstance(viewType)!;
    }

    return new TextBlock
    {
        Text = $"View not found: {viewName}"
    };
}

public bool Match(object? data)
{
    return data is ViewModelBase;
}

}
