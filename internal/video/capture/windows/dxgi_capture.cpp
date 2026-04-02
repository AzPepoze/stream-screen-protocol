#include "dxgi_capture.h"
#include <cstring>
#include <iostream>

#pragma comment(lib, "dxgi.lib")
#pragma comment(lib, "d3d11.lib")
#pragma comment(lib, "dxguid.lib")

static thread_local std::string g_lastError;

extern "C" __declspec(dllexport) const char *DXGI_GetLastError()
{
     return g_lastError.c_str();
}

void SetLastError(const std::string &error)
{
     g_lastError = error;
}

DXGICapture *DXGI_InitCapture(int outputIndex, const char *sharedMemName)
{
     HRESULT hr;

     // Initialize COM
     hr = CoInitializeEx(nullptr, COINIT_MULTITHREADED);
     if (FAILED(hr) && hr != RPC_E_CHANGED_MODE)
     {
          SetLastError("Failed to initialize COM");
          return nullptr;
     }

     // Create D3D11 device
     UINT createDeviceFlags = 0;
#ifdef _DEBUG
     createDeviceFlags |= D3D11_CREATE_DEVICE_DEBUG;
#endif

     ComPtr<ID3D11Device> device;
     ComPtr<ID3D11DeviceContext> deviceContext;

     hr = D3D11CreateDevice(
         nullptr, D3D_DRIVER_TYPE_HARDWARE, nullptr, createDeviceFlags,
         nullptr, 0, D3D11_SDK_VERSION, &device, nullptr, &deviceContext);

     if (FAILED(hr))
     {
          SetLastError("Failed to create D3D11 device: " + std::to_string(hr));
          return nullptr;
     }

     // Get DXGI device and adapter
     ComPtr<IDXGIDevice> dxgiDevice;
     hr = device->QueryInterface(__uuidof(IDXGIDevice), reinterpret_cast<void **>(&dxgiDevice));
     if (FAILED(hr))
     {
          SetLastError("Failed to get DXGI device");
          return nullptr;
     }

     ComPtr<IDXGIAdapter> adapter;
     hr = dxgiDevice->GetAdapter(&adapter);
     if (FAILED(hr))
     {
          SetLastError("Failed to get adapter");
          return nullptr;
     }

     // Get output
     ComPtr<IDXGIOutput> output;
     hr = adapter->EnumOutputs(outputIndex, &output);
     if (FAILED(hr))
     {
          SetLastError("Failed to enumerate output " + std::to_string(outputIndex));
          return nullptr;
     }

     // Get output description
     DXGI_OUTPUT_DESC outputDesc;
     output->GetDesc(&outputDesc);

     int width = outputDesc.DesktopCoordinates.right - outputDesc.DesktopCoordinates.left;
     int height = outputDesc.DesktopCoordinates.bottom - outputDesc.DesktopCoordinates.top;

     if (width <= 0 || height <= 0)
     {
          SetLastError("Invalid output dimensions");
          return nullptr;
     }

     // Get IDXGIOutput1
     ComPtr<IDXGIOutput1> output1;
     hr = output->QueryInterface(__uuidof(IDXGIOutput1), reinterpret_cast<void **>(&output1));
     if (FAILED(hr))
     {
          SetLastError("IDXGIOutput1 not available (requires Windows 8+)");
          return nullptr;
     }

     // Create duplication
     ComPtr<IDXGIOutputDuplication> duplication;
     hr = output1->DuplicateOutput(device.Get(), &duplication);
     if (FAILED(hr))
     {
          SetLastError("Failed to create desktop duplication: " + std::to_string(hr));
          return nullptr;
     }

     // Create staging texture
     D3D11_TEXTURE2D_DESC stagingDesc = {};
     stagingDesc.Width = static_cast<UINT>(width);
     stagingDesc.Height = static_cast<UINT>(height);
     stagingDesc.MipLevels = 1;
     stagingDesc.ArraySize = 1;
     stagingDesc.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
     stagingDesc.SampleDesc.Count = 1;
     stagingDesc.Usage = D3D11_USAGE_STAGING;
     stagingDesc.CPUAccessFlags = D3D11_CPU_ACCESS_READ;

     ComPtr<ID3D11Texture2D> stagingTexture;
     hr = device->CreateTexture2D(&stagingDesc, nullptr, &stagingTexture);
     if (FAILED(hr))
     {
          SetLastError("Failed to create staging texture: " + std::to_string(hr));
          return nullptr;
     }

     // Create shared memory ring buffer
     const uint32_t frameSize = width * height * 4; // BGRA8
     const uint32_t bufferCount = 8;                // 8-frame ring buffer
     const uint32_t totalSize = sizeof(SharedRingBuffer) + (frameSize * bufferCount);

     HANDLE sharedMemHandle = CreateFileMappingA(
         INVALID_HANDLE_VALUE, nullptr, PAGE_READWRITE, 0, totalSize, sharedMemName);

     if (!sharedMemHandle)
     {
          SetLastError("Failed to create shared memory: " + std::to_string(GetLastError()));
          return nullptr;
     }

     SharedRingBuffer *ringBuffer = static_cast<SharedRingBuffer *>(
         MapViewOfFile(sharedMemHandle, FILE_MAP_ALL_ACCESS, 0, 0, totalSize));

     if (!ringBuffer)
     {
          CloseHandle(sharedMemHandle);
          SetLastError("Failed to map shared memory: " + std::to_string(GetLastError()));
          return nullptr;
     }

     // Initialize ring buffer
     ringBuffer->writeIndex = 0;
     ringBuffer->readIndex = 0;
     ringBuffer->bufferCount = bufferCount;
     ringBuffer->frameSize = frameSize;

     // Create capture handle
     DXGICapture *handle = new DXGICapture();
     handle->device = device;
     handle->deviceContext = deviceContext;
     handle->duplication = duplication;
     handle->stagingTexture = stagingTexture;
     handle->sharedMemHandle = sharedMemHandle;
     handle->ringBuffer = ringBuffer;
     handle->bufferSize = totalSize;
     handle->frameSize = frameSize;
     handle->running = false;
     handle->width = width;
     handle->height = height;

     return handle;
}

void CaptureThread(DXGICapture *handle)
{
     const int ACQUIRE_TIMEOUT_MS = 500;

     while (handle->running)
     {
          HRESULT hr;

          // Acquire next frame
          ComPtr<IDXGIResource> desktopResource;
          DXGI_OUTDUPL_FRAME_INFO frameInfo = {};

          hr = handle->duplication->AcquireNextFrame(ACQUIRE_TIMEOUT_MS, &frameInfo, &desktopResource);

          if (hr == DXGI_ERROR_WAIT_TIMEOUT)
          {
               continue; // No new frame, try again
          }

          if (FAILED(hr))
          {
               if (hr == DXGI_ERROR_ACCESS_LOST)
               {
                    std::this_thread::sleep_for(std::chrono::milliseconds(100));
                    continue; // Try to recover
               }
               break; // Fatal error
          }

          // Get desktop texture
          ComPtr<ID3D11Texture2D> desktopTexture;
          hr = desktopResource->QueryInterface(__uuidof(ID3D11Texture2D),
                                               reinterpret_cast<void **>(&desktopTexture));

          if (FAILED(hr))
          {
               handle->duplication->ReleaseFrame();
               continue;
          }

          // Copy to staging texture
          handle->deviceContext->CopyResource(handle->stagingTexture.Get(), desktopTexture.Get());

          // Map staging texture
          D3D11_MAPPED_SUBRESOURCE mappedResource;
          hr = handle->deviceContext->Map(handle->stagingTexture.Get(), 0, D3D11_MAP_READ, 0, &mappedResource);

          if (FAILED(hr))
          {
               handle->duplication->ReleaseFrame();
               continue;
          }

          // Write to ring buffer
          uint32_t writeIndex = handle->ringBuffer->writeIndex.load(std::memory_order_acquire);
          uint32_t nextWriteIndex = (writeIndex + 1) % handle->ringBuffer->bufferCount;

          // Check if buffer is full (simple check - in production you'd want overflow handling)
          if (nextWriteIndex == handle->ringBuffer->readIndex.load(std::memory_order_acquire))
          {
               handle->deviceContext->Unmap(handle->stagingTexture.Get(), 0);
               handle->duplication->ReleaseFrame();
               std::this_thread::sleep_for(std::chrono::milliseconds(1));
               continue;
          }

          // Copy frame data to ring buffer
          uint8_t *dstPtr = handle->ringBuffer->data + (writeIndex * handle->frameSize);
          uint8_t *srcPtr = static_cast<uint8_t *>(mappedResource.pData);

          for (int y = 0; y < handle->height; ++y)
          {
               std::memcpy(dstPtr, srcPtr, handle->width * 4);
               dstPtr += handle->width * 4;
               srcPtr += mappedResource.RowPitch;
          }

          handle->deviceContext->Unmap(handle->stagingTexture.Get(), 0);
          handle->duplication->ReleaseFrame();

          // Update write index
          handle->ringBuffer->writeIndex.store(nextWriteIndex, std::memory_order_release);
     }
}

bool DXGI_StartCapture(DXGICapture *handle)
{
     if (!handle || handle->running)
     {
          return false;
     }

     handle->running = true;
     handle->captureThread = std::thread(CaptureThread, handle);
     return true;
}

void DXGI_StopCapture(DXGICapture *handle)
{
     if (!handle)
          return;

     handle->running = false;
     if (handle->captureThread.joinable())
     {
          handle->captureThread.join();
     }
}

void DXGI_Release(DXGICapture *handle)
{
     if (!handle)
          return;

     DXGI_StopCapture(handle);

     if (handle->ringBuffer)
     {
          UnmapViewOfFile(handle->ringBuffer);
     }

     if (handle->sharedMemHandle)
     {
          CloseHandle(handle->sharedMemHandle);
     }

     delete handle;
     CoUninitialize();
}

bool DXGI_HasNewFrame(DXGICapture *handle)
{
     if (!handle || !handle->ringBuffer)
          return false;

     uint32_t writeIndex = handle->ringBuffer->writeIndex.load(std::memory_order_acquire);
     uint32_t readIndex = handle->ringBuffer->readIndex.load(std::memory_order_acquire);

     return writeIndex != readIndex;
}

const uint8_t *DXGI_GetFrameData(DXGICapture *handle, FrameMetadata *metadata)
{
     if (!handle || !handle->ringBuffer || !DXGI_HasNewFrame(handle))
     {
          return nullptr;
     }

     uint32_t readIndex = handle->ringBuffer->readIndex.load(std::memory_order_acquire);

     if (metadata)
     {
          metadata->width = handle->width;
          metadata->height = handle->height;
          metadata->timestamp = GetTickCount64();
          metadata->frameSize = handle->frameSize;
     }

     return handle->ringBuffer->data + (readIndex * handle->frameSize);
}

void DXGI_ReleaseFrame(DXGICapture *handle)
{
     if (!handle || !handle->ringBuffer)
          return;

     uint32_t readIndex = handle->ringBuffer->readIndex.load(std::memory_order_acquire);
     uint32_t nextReadIndex = (readIndex + 1) % handle->ringBuffer->bufferCount;

     handle->ringBuffer->readIndex.store(nextReadIndex, std::memory_order_release);
}