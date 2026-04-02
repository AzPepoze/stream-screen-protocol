#include "dxgi.h"
#include <string>
#include <cstring>
#include <cstdint>
#include <memory>
#include <objbase.h>

#pragma comment(lib, "dxgi.lib")
#pragma comment(lib, "d3d11.lib")
#pragma comment(lib, "dxguid.lib")

// Constants
static constexpr int ACQUIRE_FRAME_TIMEOUT_MS = 500;
static constexpr int BYTES_PER_PIXEL = 4; // BGRA8

// Thread-local error storage for thread safety
static thread_local std::string g_lastError;

extern "C"
{

     const char *DXGI_GetLastError()
     {
          return g_lastError.c_str();
     }

     void SetError(const std::string &message)
     {
          g_lastError = message;
     }

     // RAII wrapper for COM initialization
     class ComInitializer
     {
     public:
          ComInitializer()
          {
               HRESULT hr = CoInitializeEx(nullptr, COINIT_MULTITHREADED);
               if (FAILED(hr) && hr != RPC_E_CHANGED_MODE)
               {
                    SetError("Failed to initialize COM");
               }
          }

          ~ComInitializer()
          {
               CoUninitialize();
          }
     };

     // Simple RAII scope guard
     template <typename F>
     class ScopeGuard
     {
     public:
          explicit ScopeGuard(F &&f) : func(std::move(f)), active(true) {}
          ~ScopeGuard()
          {
               if (active)
                    func();
          }
          void dismiss() { active = false; }

     private:
          F func;
          bool active;
     };

     DXGIHandle *DXGI_InitCapture(int outputIndex)
     {
          static ComInitializer comInit; // Initialize COM once per process

          HRESULT hr;

          // Create D3D11 device with debug layer in debug builds
          UINT createDeviceFlags = 0;
#ifdef _DEBUG
          createDeviceFlags |= D3D11_CREATE_DEVICE_DEBUG;
#endif

          ComPtr<ID3D11Device> device;
          ComPtr<ID3D11DeviceContext> deviceContext;

          hr = D3D11CreateDevice(
              nullptr,                  // Default adapter
              D3D_DRIVER_TYPE_HARDWARE, // Hardware acceleration
              nullptr,                  // No software rasterizer
              createDeviceFlags,        // Debug flags
              nullptr,                  // Feature levels (use defaults)
              0,                        // Number of feature levels
              D3D11_SDK_VERSION,        // SDK version
              &device,                  // Device output
              nullptr,                  // Feature level output
              &deviceContext            // Context output
          );

          if (FAILED(hr))
          {
               SetError("Failed to create D3D11 device: " + std::to_string(hr));
               return nullptr;
          }

          // Get DXGI device interface
          ComPtr<IDXGIDevice> dxgiDevice;
          hr = device->QueryInterface(__uuidof(IDXGIDevice), reinterpret_cast<void **>(&dxgiDevice));
          if (FAILED(hr))
          {
               SetError("Failed to get DXGI device interface: " + std::to_string(hr));
               return nullptr;
          }

          // Get the adapter
          ComPtr<IDXGIAdapter> adapter;
          hr = dxgiDevice->GetAdapter(&adapter);
          if (FAILED(hr))
          {
               SetError("Failed to get adapter: " + std::to_string(hr));
               return nullptr;
          }

          // Enumerate the specified output (monitor)
          ComPtr<IDXGIOutput> output;
          hr = adapter->EnumOutputs(outputIndex, &output);
          if (FAILED(hr))
          {
               SetError("Failed to enumerate output " + std::to_string(outputIndex) + ": " + std::to_string(hr));
               return nullptr;
          }

          // Get output description to determine resolution
          DXGI_OUTPUT_DESC outputDesc;
          output->GetDesc(&outputDesc);

          int width = outputDesc.DesktopCoordinates.right - outputDesc.DesktopCoordinates.left;
          int height = outputDesc.DesktopCoordinates.bottom - outputDesc.DesktopCoordinates.top;

          if (width <= 0 || height <= 0)
          {
               SetError("Invalid output dimensions: " + std::to_string(width) + "x" + std::to_string(height));
               return nullptr;
          }

          // Get IDXGIOutput1 for desktop duplication (requires Windows 8+)
          ComPtr<IDXGIOutput1> output1;
          hr = output->QueryInterface(__uuidof(IDXGIOutput1), reinterpret_cast<void **>(&output1));
          if (FAILED(hr))
          {
               SetError("IDXGIOutput1 not available (requires Windows 8+): " + std::to_string(hr));
               return nullptr;
          }

          // Create desktop duplication interface
          ComPtr<IDXGIOutputDuplication> duplication;
          hr = output1->DuplicateOutput(device.Get(), &duplication);
          if (FAILED(hr))
          {
               SetError("Failed to create desktop duplication: " + std::to_string(hr));
               return nullptr;
          }

          // Create staging texture for CPU readback
          D3D11_TEXTURE2D_DESC stagingDesc = {};
          stagingDesc.Width = static_cast<UINT>(width);
          stagingDesc.Height = static_cast<UINT>(height);
          stagingDesc.MipLevels = 1;
          stagingDesc.ArraySize = 1;
          stagingDesc.Format = DXGI_FORMAT_B8G8R8A8_UNORM; // BGRA8 format
          stagingDesc.SampleDesc.Count = 1;
          stagingDesc.SampleDesc.Quality = 0;
          stagingDesc.Usage = D3D11_USAGE_STAGING;
          stagingDesc.BindFlags = 0;
          stagingDesc.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
          stagingDesc.MiscFlags = 0;

          ComPtr<ID3D11Texture2D> stagingTexture;
          hr = device->CreateTexture2D(&stagingDesc, nullptr, &stagingTexture);
          if (FAILED(hr))
          {
               SetError("Failed to create staging texture: " + std::to_string(hr));
               return nullptr;
          }

          // Create handle and transfer ownership
          DXGIHandle *handle = new (std::nothrow) DXGIHandle();
          if (!handle)
          {
               SetError("Failed to allocate DXGIHandle");
               return nullptr;
          }

          handle->device = device.Detach();
          handle->deviceContext = deviceContext.Detach();
          handle->duplication = duplication.Detach();
          handle->stagingTexture = stagingTexture.Detach();
          handle->width = width;
          handle->height = height;

          return handle;
     }

     DXGIFrame *DXGI_CaptureFrame(DXGIHandle *handle)
     {
          if (!handle)
          {
               SetError("Handle is null");
               return nullptr;
          }

          // Cast back to COM interfaces (safe since we stored them as void*)
          ID3D11DeviceContext *deviceContext = static_cast<ID3D11DeviceContext *>(handle->deviceContext);
          IDXGIOutputDuplication *duplication = static_cast<IDXGIOutputDuplication *>(handle->duplication);
          ID3D11Texture2D *stagingTexture = static_cast<ID3D11Texture2D *>(handle->stagingTexture);

          if (!deviceContext || !duplication || !stagingTexture)
          {
               SetError("Invalid handle state - missing COM interfaces");
               return nullptr;
          }

          HRESULT hr;

          // Acquire the next frame
          ComPtr<IDXGIResource> desktopResource;
          DXGI_OUTDUPL_FRAME_INFO frameInfo = {};

          hr = duplication->AcquireNextFrame(ACQUIRE_FRAME_TIMEOUT_MS, &frameInfo, &desktopResource);
          if (hr == DXGI_ERROR_WAIT_TIMEOUT)
          {
               // No new frame available within timeout - this is normal
               return nullptr;
          }

          if (FAILED(hr))
          {
               if (hr == DXGI_ERROR_ACCESS_LOST)
               {
                    SetError("Desktop duplication access lost - monitor configuration may have changed");
               }
               else
               {
                    SetError("Failed to acquire next frame: " + std::to_string(hr));
               }
               return nullptr;
          }

          // Ensure we release the frame even if errors occur
          ScopeGuard frameGuard([&]()
                                { duplication->ReleaseFrame(); });

          // Get the desktop texture from the resource
          ComPtr<ID3D11Texture2D> desktopTexture;
          hr = desktopResource->QueryInterface(__uuidof(ID3D11Texture2D), reinterpret_cast<void **>(&desktopTexture));
          if (FAILED(hr))
          {
               SetError("Failed to get desktop texture: " + std::to_string(hr));
               return nullptr;
          }

          // Copy the desktop texture to our staging texture for CPU access
          deviceContext->CopyResource(stagingTexture, desktopTexture.Get());

          // Map the staging texture to read the pixel data
          D3D11_MAPPED_SUBRESOURCE mappedResource;
          hr = deviceContext->Map(stagingTexture, 0, D3D11_MAP_READ, 0, &mappedResource);
          if (FAILED(hr))
          {
               SetError("Failed to map staging texture: " + std::to_string(hr));
               return nullptr;
          }

          // Ensure we unmap the texture
          ScopeGuard mapGuard([&]()
                              { deviceContext->Unmap(stagingTexture, 0); });

          // Calculate frame size and allocate buffer
          size_t frameSize = static_cast<size_t>(handle->width) * handle->height * BYTES_PER_PIXEL;
          std::unique_ptr<unsigned char[]> frameData(new (std::nothrow) unsigned char[frameSize]);
          if (!frameData)
          {
               SetError("Failed to allocate frame buffer");
               return nullptr;
          }

          // Copy the frame data row by row (handling potential row padding)
          unsigned char *dstPtr = frameData.get();
          unsigned char *srcPtr = static_cast<unsigned char *>(mappedResource.pData);
          size_t bytesPerRow = static_cast<size_t>(handle->width) * BYTES_PER_PIXEL;

          for (int y = 0; y < handle->height; ++y)
          {
               std::memcpy(dstPtr, srcPtr, bytesPerRow);
               dstPtr += bytesPerRow;
               srcPtr += mappedResource.RowPitch;
          }

          // Create frame object and transfer ownership of the data
          DXGIFrame *frame = new (std::nothrow) DXGIFrame();
          if (!frame)
          {
               SetError("Failed to allocate DXGIFrame");
               return nullptr;
          }

          frame->data = frameData.release(); // Transfer ownership
          frame->size = static_cast<int>(frameSize);
          frame->width = handle->width;
          frame->height = handle->height;

          return frame;
     }

     void DXGI_FreeFrame(DXGIFrame *frame)
     {
          if (frame)
          {
               delete[] frame->data;
               delete frame;
          }
     }

     void DXGI_Release(DXGIHandle *handle)
     {
          if (!handle)
          {
               return;
          }

          // Release COM objects in reverse order of creation
          if (handle->stagingTexture)
          {
               static_cast<ID3D11Texture2D *>(handle->stagingTexture)->Release();
          }
          if (handle->duplication)
          {
               static_cast<IDXGIOutputDuplication *>(handle->duplication)->Release();
          }
          if (handle->deviceContext)
          {
               static_cast<ID3D11DeviceContext *>(handle->deviceContext)->Release();
          }
          if (handle->device)
          {
               static_cast<ID3D11Device *>(handle->device)->Release();
          }

          delete handle;
     }

} // extern "C"
