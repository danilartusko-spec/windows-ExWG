#include <flutter/dart_project.h>
#include <flutter/flutter_view_controller.h>
#include <shobjidl.h>
#include <windows.h>

#include "flutter_window.h"
#include "utils.h"

int APIENTRY wWinMain(_In_ HINSTANCE instance, _In_opt_ HINSTANCE prev,
                      _In_ wchar_t *command_line, _In_ int show_command) {
  // Give the shell an explicit identity instead of letting it infer one from
  // the executable path. This keeps the running taskbar icon aligned with the
  // shortcut after upgrading an older Ex WG installation.
  constexpr wchar_t kAppUserModelId[] = L"ExWG.Client.Windows";
  ::SetCurrentProcessExplicitAppUserModelID(kAppUserModelId);

  HANDLE single_instance = ::CreateMutexW(
      nullptr, TRUE, L"Local\\ExWG.Client.Windows.SingleInstance");
  if (!single_instance) {
    return EXIT_FAILURE;
  }
  if (::GetLastError() == ERROR_ALREADY_EXISTS) {
    constexpr wchar_t kWindowClassName[] = L"FLUTTER_RUNNER_WIN32_WINDOW";
    HWND existing = nullptr;
    for (int attempt = 0; attempt < 40 && !existing; ++attempt) {
      existing = ::FindWindowW(kWindowClassName, L"Ex WG");
      if (!existing) {
        ::Sleep(50);
      }
    }
    if (existing) {
      constexpr UINT kRestoreWindowMessage = WM_APP + 2;
      ::PostMessageW(existing, kRestoreWindowMessage, 0, 0);
    }
    ::CloseHandle(single_instance);
    return EXIT_SUCCESS;
  }

  // Attach to console when present (e.g., 'flutter run') or create a
  // new console when running with a debugger.
  if (!::AttachConsole(ATTACH_PARENT_PROCESS) && ::IsDebuggerPresent()) {
    CreateAndAttachConsole();
  }

  // Initialize COM, so that it is available for use in the library and/or
  // plugins.
  ::CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED);

  flutter::DartProject project(L"data");

  std::vector<std::string> command_line_arguments =
      GetCommandLineArguments();

  project.set_dart_entrypoint_arguments(std::move(command_line_arguments));

  FlutterWindow window(project);
  constexpr int width = 1080;
  constexpr int height = 720;
  const int left = (::GetSystemMetrics(SM_CXSCREEN) - width) / 2;
  const int top = (::GetSystemMetrics(SM_CYSCREEN) - height) / 2;
  Win32Window::Point origin(left > 0 ? left : 0, top > 0 ? top : 0);
  Win32Window::Size size(width, height);
  if (!window.Create(L"Ex WG", origin, size)) {
    ::CloseHandle(single_instance);
    return EXIT_FAILURE;
  }
  window.SetQuitOnClose(true);

  ::MSG msg;
  while (::GetMessage(&msg, nullptr, 0, 0)) {
    ::TranslateMessage(&msg);
    ::DispatchMessage(&msg);
  }

  // Ask ExWGCore to remove the tunnel and wait for it, so Windows shutdown
  // or a tray exit never freezes a half-removed Nexus adapter.
  if (HANDLE shutdown_event = ::OpenEventW(
          EVENT_MODIFY_STATE, FALSE,
          L"Local\\ExWG.Client.Windows.CoreShutdown")) {
    ::SetEvent(shutdown_event);
    ::CloseHandle(shutdown_event);
  }
  if (HANDLE core_mutex = ::OpenMutexW(
          SYNCHRONIZE, FALSE,
          L"Local\\ExWG.Client.Windows.CoreInstance")) {
    const DWORD result = ::WaitForSingleObject(core_mutex, 10000);
    if (result == WAIT_OBJECT_0 || result == WAIT_ABANDONED) {
      ::ReleaseMutex(core_mutex);
    }
    ::CloseHandle(core_mutex);
  }

  ::CoUninitialize();
  ::ReleaseMutex(single_instance);
  ::CloseHandle(single_instance);
  return EXIT_SUCCESS;
}
