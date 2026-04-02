#pragma once

#include <windows.h>
#include <dxgi1_2.h>
#include <d3d11.h>
#include <wrl/client.h>
#include <memory>
#include <string>
#include <atomic>
#include <thread>
#include <chrono>

using Microsoft::WRL::ComPtr;

// Shared memory ring buffer for frame data
struct SharedRingBuffer
{
     std::atomic<uint32_t> writeIndex;
     std::atomic<uint32_t> readIndex;
     uint32_t bufferCount;
     uint32_t frameSize;
     uint8_t data[1]; // Variable size
};

// Frame metadata
struct FrameMetadata
{
     uint32_t width;
     uint32_t height;
     uint64_t timestamp;
     uint32_t frameSize;
};

// Capture handle
struct DXGICapture
{
     ComPtr<ID3D11Device> device;
     ComPtr<ID3D11DeviceContext> deviceContext;
     ComPtr<IDXGIOutputDuplication> duplication;
     ComPtr<ID3D11Texture2D> stagingTexture;

     HANDLE sharedMemHandle;
     SharedRingBuffer *ringBuffer;
     uint32_t bufferSize;
     uint32_t frameSize;

     std::atomic<bool> running;
     std::thread captureThread;

     int width;
     int height;
};

// Error handling
extern "C" __declspec(dllexport) const char *DXGI_GetLastError();
void SetLastError(const std::string &error);

// Capture functions
extern "C" __declspec(dllexport) DXGICapture *DXGI_InitCapture(int outputIndex, const char *sharedMemName);
extern "C" __declspec(dllexport) bool DXGI_StartCapture(DXGICapture *handle);
extern "C" __declspec(dllexport) void DXGI_StopCapture(DXGICapture *handle);
extern "C" __declspec(dllexport) void DXGI_Release(DXGICapture *handle);

// Frame access functions
extern "C" __declspec(dllexport) bool DXGI_HasNewFrame(DXGICapture *handle);
extern "C" __declspec(dllexport) const uint8_t *DXGI_GetFrameData(DXGICapture *handle, FrameMetadata *metadata);
extern "C" __declspec(dllexport) void DXGI_ReleaseFrame(DXGICapture *handle);