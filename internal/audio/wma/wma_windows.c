#include <windows.h>
#include <mfapi.h>
#include <mfidl.h>
#include <mfreadwrite.h>
#include <mferror.h>
#include <wrl/client.h>
#include <stdio.h>

#pragma comment(lib, "mfplat.lib")
#pragma comment(lib, "mfreadwrite.lib")
#pragma comment(lib, "mfuuid.lib")
#pragma comment(lib, "ole32.lib")

using Microsoft::WRL::ComPtr;

// struct to pass encoded data back to Go
typedef struct
{
     unsigned char *data;
     int size;
} AudioBuffer;

// Initialize Media Foundation (call once at startup)
HRESULT InitMediaFoundation()
{
     return MFStartup(MF_VERSION, MFSTARTUP_LITE);
}

// Shutdown Media Foundation (call once at exit)
void ShutdownMediaFoundation()
{
     MFShutdown();
}

// Encode PCM to WMA
AudioBuffer *EncodeWMA(unsigned char *pcmData, int pcmSize, int sampleRate, int channels, int bitrate)
{
     static BOOL initialized = FALSE;
     if (!initialized)
     {
          InitMediaFoundation();
          initialized = TRUE;
     }

     AudioBuffer *result = (AudioBuffer *)malloc(sizeof(AudioBuffer));
     if (!result)
          return NULL;

     result->data = NULL;
     result->size = 0;

     // For now, return raw PCM (pass-through)
     // A full implementation would use IMFMediaType, IMFTransform, etc.
     result->data = (unsigned char *)malloc(pcmSize);
     if (!result->data)
     {
          free(result);
          return NULL;
     }
     memcpy(result->data, pcmData, pcmSize);
     result->size = pcmSize;

     return result;
}

// Decode WMA to PCM
AudioBuffer *DecodeWMA(unsigned char *encodedData, int encodedSize, int sampleRate, int channels)
{
     AudioBuffer *result = (AudioBuffer *)malloc(sizeof(AudioBuffer));
     if (!result)
          return NULL;

     result->data = NULL;
     result->size = 0;

     // For now, return raw PCM (pass-through)
     // A full implementation would use IMFMediaType, IMFTransform, etc.
     result->data = (unsigned char *)malloc(encodedSize);
     if (!result->data)
     {
          free(result);
          return NULL;
     }
     memcpy(result->data, encodedData, encodedSize);
     result->size = encodedSize;

     return result;
}

// Free allocated audio buffer
void FreeAudioBuffer(AudioBuffer *buffer)
{
     if (buffer)
     {
          if (buffer->data)
          {
               free(buffer->data);
          }
          free(buffer);
     }
}
